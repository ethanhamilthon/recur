package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"recur/internal/agent"
	"recur/internal/control"
	"recur/internal/repl"
	"recur/internal/tui"
)

// The JSON transport is newline-delimited UTF-8 JSON in both directions. Every
// request carries an id and receives exactly one result. Controller events
// follow in emission order. Stdout carries only protocol lines; diagnostics go
// to stderr.
const (
	protocolVersion   = 1
	maxRecordBytes    = 2 * 1024 * 1024
	outboxLimit       = 4096
	maxAsyncRequests  = 8
	maxImagesPerInput = 8
	shutdownGrace     = 10 * time.Second
	flushGrace        = 2 * time.Second // how long a closing transport waits for stdout to drain
)

// request is one stdin record. Images and files are paths resolved by the transport.
type request struct {
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Text   string            `json:"text"`
	Name   string            `json:"name"`
	Images []string          `json:"images"`
	Files  []string          `json:"files"`
	Args   map[string]string `json:"args"`
}

// serveJSON runs the transport until stdin ends, the controller closes, or the
// process is interrupted. Only the controller changes application state; this
// function frames records, writes results and events, and tracks listings.
func serveJSON(ctx context.Context, a *agent.Agent, cwd string, in io.Reader, out, diagnostics io.Writer) error {
	t := newTransport(out)
	c := control.New(a, func(e control.Event) { t.emit(e) })
	c.SetInteractive(false)
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	go func() { _ = c.Run(runCtx) }()
	defer t.close()

	t.emit(readyRecord(c, cwd))
	records := make(chan inputRecord)
	stopReader := make(chan struct{})
	defer close(stopReader)
	go readRecords(in, records, stopReader)

	pending := newInFlight()
	for {
		select {
		case <-ctx.Done():
			interrupt(c)
			t.finish("signal")
			return ctx.Err()
		case <-t.broken:
			interrupt(c)
			t.finish("broken_output")
			return fmt.Errorf("writing protocol output: %w", t.failure())
		case <-t.overflow:
			interrupt(c)
			t.finish("output_overflow")
			fmt.Fprintln(diagnostics, "recur: JSON output backlog exceeded; the session was stopped")
			return errors.New("JSON output backlog exceeded")
		case <-c.Closed():
			t.finish(c.Reason())
			return nil
		case rec, ok := <-records:
			if !ok {
				// Stdin ended: accepted work drains, then the session saves.
				waitCtx, cancel := context.WithTimeout(ctx, shutdownGrace)
				if err := c.Wait(waitCtx); err != nil {
					interrupt(c)
				}
				cancel()
				err := c.Shutdown("eof")
				t.finish("eof")
				if err == nil && t.failure() != nil {
					err = fmt.Errorf("writing protocol output: %w", t.failure())
				}
				return err
			}
			handleRecord(c, cwd, rec, t, pending)
		}
	}
}

// interrupt stops active work so its turn ends with a cancelled done event.
func interrupt(c *control.Controller) {
	_, _ = c.Submit(control.Request{Type: "stop"})
	waitCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	_ = c.Wait(waitCtx)
}

// handleRecord decodes one stdin record and answers it. Model listings run in
// the background so stop and steering stay responsive while they wait.
func handleRecord(c *control.Controller, cwd string, rec inputRecord, t *transport, pending *inFlight) {
	if rec.tooLong {
		t.protocolError("record_too_large", fmt.Sprintf("input record exceeds %d bytes", maxRecordBytes))
		return
	}
	var req request
	if err := json.Unmarshal(rec.line, &req); err != nil {
		t.protocolError("invalid_json", "input record is not a valid JSON object")
		return
	}
	if req.ID == "" {
		t.protocolError("missing_id", "every request needs a non-empty id")
		return
	}
	if !pending.begin(req.ID, req.Type == "models") {
		busy := &control.Error{Code: "busy", Message: "too many model listings are in flight"}
		code := &control.Error{Code: "duplicate_id", Message: "a request with this id is still in flight"}
		if pending.has(req.ID) {
			t.result(req.ID, control.Response{}, code)
		} else {
			t.result(req.ID, control.Response{}, busy)
		}
		return
	}
	if req.Type == "models" {
		go func() {
			defer pending.end(req.ID, true)
			resp, err := c.Submit(control.Request{Type: "models"})
			t.result(req.ID, resp, err)
		}()
		return
	}
	defer pending.end(req.ID, false)
	prepared, err := prepare(c, cwd, req)
	if err != nil {
		t.result(req.ID, control.Response{}, err)
		return
	}
	resp, err := c.Submit(prepared)
	t.result(req.ID, resp, err)
}

// prepare converts paths into image and file snapshots before the controller sees the request.
func prepare(c *control.Controller, cwd string, req request) (control.Request, error) {
	settings := c.Settings()
	out := control.Request{Type: req.Type, Text: req.Text, Name: req.Name, Args: req.Args}
	if len(req.Images) > maxImagesPerInput {
		return out, &control.Error{Code: "attachment_limit", Message: fmt.Sprintf("at most %d images per request", maxImagesPerInput)}
	}
	for _, path := range req.Images {
		image, err := repl.CaptureImageCopy(resolvePath(cwd, path), attachmentDirectory(settings, cwd))
		if err != nil {
			return out, &control.Error{Code: "invalid_image", Message: fmt.Sprintf("%s: %v", path, err)}
		}
		out.Images = append(out.Images, image)
	}
	if len(req.Files) > 0 {
		files, err := agent.ReadTextFiles(cwd, req.Files, settings.MaxFileBytes)
		if err != nil {
			return out, &control.Error{Code: "invalid_file", Message: err.Error()}
		}
		out.Files = files
	}
	return out, nil
}

// resolvePath resolves relative paths against the project directory.
func resolvePath(cwd, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

// attachmentDirectory keeps image snapshots where recur view keeps them.
func attachmentDirectory(settings agent.Settings, cwd string) string {
	if dir := settings.AttachmentDir; dir != "" {
		return resolvePath(cwd, dir)
	}
	return filepath.Join(recurHome(), "attachments")
}

// recurHome is $RECUR_HOME, or ~/.recur when unset.
func recurHome() string {
	if home := os.Getenv("RECUR_HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".recur"
	}
	return filepath.Join(home, ".recur")
}

func readyRecord(c *control.Controller, cwd string) map[string]any {
	resp, _ := c.Submit(control.Request{Type: "state"})
	return map[string]any{
		"type":     "ready",
		"protocol": protocolVersion,
		"version":  strings.TrimPrefix(tui.Version, "v"),
		"cwd":      cwd,
		"state":    resp.Data,
		"capabilities": map[string]any{
			"requests":         control.Types,
			"max_record_bytes": maxRecordBytes,
			"max_images":       maxImagesPerInput,
			"max_file_bytes":   c.Settings().MaxFileBytes,
		},
	}
}

// inputRecord is one framed stdin line; tooLong marks a record that was discarded.
type inputRecord struct {
	line    []byte
	tooLong bool
}

// readRecords frames newline-delimited records without buffering more than
// maxRecordBytes of one line. An oversized line is reported and skipped, so the
// stream stays in sync for the next request.
func readRecords(in io.Reader, out chan<- inputRecord, stop <-chan struct{}) {
	defer close(out)
	reader := bufio.NewReaderSize(in, 64*1024)
	for {
		line, tooLong, err := readRecord(reader)
		if len(bytes.TrimSpace(line)) > 0 || tooLong {
			select {
			case out <- inputRecord{line: line, tooLong: tooLong}:
			case <-stop:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// readRecord returns the next line without its terminator. Bytes beyond the
// limit are discarded while the rest of the line is consumed.
func readRecord(reader *bufio.Reader) ([]byte, bool, error) {
	var line []byte
	tooLong := false
	for {
		chunk, err := reader.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > maxRecordBytes+2 {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > maxRecordBytes {
			tooLong, line = true, nil
		}
		return line, tooLong, err
	}
}

// inFlight tracks request ids whose results are still pending, and how many model listings run.
type inFlight struct {
	mu       sync.Mutex
	ids      map[string]bool
	listings int
}

func newInFlight() *inFlight { return &inFlight{ids: map[string]bool{}} }

// begin reserves id; it fails for a duplicate id or when too many listings run.
func (f *inFlight) begin(id string, listing bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ids[id] || (listing && f.listings >= maxAsyncRequests) {
		return false
	}
	f.ids[id] = true
	if listing {
		f.listings++
	}
	return true
}

func (f *inFlight) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ids[id]
}

func (f *inFlight) end(id string, listing bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.ids, id)
	if listing {
		f.listings--
	}
}

// transport is the single writer of stdout. Emission never blocks: a full
// backlog is reported through overflow so the controller can stop safely.
type transport struct {
	out      io.Writer
	mu       sync.Mutex
	seq      int
	closed   bool
	ended    bool
	err      error
	ch       chan map[string]any
	done     chan struct{}
	broken   chan struct{}
	overflow chan struct{}
}

func newTransport(out io.Writer) *transport {
	t := &transport{
		out:      out,
		ch:       make(chan map[string]any, outboxLimit),
		done:     make(chan struct{}),
		broken:   make(chan struct{}),
		overflow: make(chan struct{}, 1),
	}
	go t.write()
	return t
}

// write encodes records in order; after a write failure it drains the backlog silently.
func (t *transport) write() {
	defer close(t.done)
	enc := json.NewEncoder(t.out)
	enc.SetEscapeHTML(false)
	failed := false
	for record := range t.ch {
		if !failed {
			if err := enc.Encode(record); err != nil {
				failed = true
				t.mu.Lock()
				t.err = err
				t.mu.Unlock()
				close(t.broken)
			}
		}
	}
}

func (t *transport) failure() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// emit queues one protocol line, assigning the next sequence number.
func (t *transport) emit(v any) {
	record := toRecord(v)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.seq++
	record["seq"] = t.seq
	select {
	case t.ch <- record:
	default:
		t.seq--
		select {
		case t.overflow <- struct{}{}:
		default:
		}
	}
}

// close stops accepting records and waits briefly for the writer to flush. It is
// safe to call more than once; only the first call waits.
func (t *transport) close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	close(t.ch)
	t.mu.Unlock()
	select {
	case <-t.done:
	case <-time.After(flushGrace):
	}
}

// finish writes the shutdown record once and closes the transport.
func (t *transport) finish(reason string) {
	t.mu.Lock()
	ended := t.ended
	t.ended = true
	t.mu.Unlock()
	if !ended {
		t.emit(map[string]any{"type": "shutdown", "reason": reason})
	}
	t.close()
}

// result answers one request: a status and payload on success, a code and message otherwise.
func (t *transport) result(id string, resp control.Response, err error) {
	record := map[string]any{"type": "result", "id": id}
	if err != nil {
		record["ok"] = false
		record["error"] = errorBody(err)
		t.emit(record)
		return
	}
	record["ok"] = true
	record["status"] = resp.Status
	if resp.Turn != "" {
		record["turn"] = resp.Turn
	}
	if resp.Input != nil {
		record["input"] = wireInput(*resp.Input)
	}
	if resp.Data != nil {
		record["data"] = resp.Data
	}
	t.emit(record)
}

func (t *transport) protocolError(code, message string) {
	t.emit(map[string]any{"type": "error", "error": map[string]string{"code": code, "message": message}})
}

// errorBody reports a rejection without credentials; unexpected errors use a generic code.
func errorBody(err error) map[string]string {
	var e *control.Error
	if errors.As(err, &e) {
		return map[string]string{"code": e.Code, "message": e.Message}
	}
	return map[string]string{"code": "failed", "message": err.Error()}
}

// wireInput names an input by its text and by the paths of its snapshots, so a
// client can resubmit it.
func wireInput(in agent.Input) map[string]any {
	images, files := []string{}, []string{}
	for _, image := range in.Images {
		images = append(images, image.Path)
	}
	for _, file := range in.Files {
		files = append(files, file.Path)
	}
	return map[string]any{"text": in.Text, "images": images, "files": files}
}

// toRecord converts controller values into protocol records.
func toRecord(v any) map[string]any {
	if record, ok := v.(map[string]any); ok {
		return record
	}
	e, ok := v.(control.Event)
	if !ok {
		return map[string]any{"type": "error", "error": map[string]string{"code": "internal", "message": fmt.Sprint(v)}}
	}
	switch e.Kind {
	case "started":
		return map[string]any{"type": "started", "turn": e.Turn, "operation": e.Operation, "source": e.Source}
	case "agent":
		return map[string]any{"type": "event", "turn": e.Turn, "event": e.Agent}
	case "notice":
		return map[string]any{"type": "notice", "turn": e.Turn, "text": e.Text}
	case "error":
		return map[string]any{"type": "error", "turn": e.Turn, "error": map[string]string{"code": "failed", "message": e.Text}}
	case "done":
		unsent := []map[string]any{}
		for _, in := range e.Unsent {
			unsent = append(unsent, wireInput(in))
		}
		return map[string]any{"type": "done", "turn": e.Turn, "operation": e.Operation, "status": e.Status,
			"error": e.Error, "message": e.Message, "unsent": unsent}
	case "session":
		return map[string]any{"type": "session", "session": e.Session}
	}
	return map[string]any{"type": "error", "error": map[string]string{"code": "internal", "message": "unknown event " + e.Kind}}
}
