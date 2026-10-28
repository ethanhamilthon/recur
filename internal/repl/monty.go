package repl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode"

	monty "github.com/ewhauser/gomonty"
)

// Monty runs in a child copy of recur, without CPython.
// Keeping the VM in a process allows hard cancellation of infinite Python loops.
func init() {
	if len(os.Args) == 2 && os.Args[1] == "--recur-worker" {
		if err := runMontyWorker(os.Stdin, os.NewFile(3, "protocol")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

// Native execution stays in the existing disposable worker process. A Go
// context cannot preempt native code; the parent still kills the process group.
func runMontyWorker(input io.Reader, output io.Writer) error {
	session, err := monty.NewRepl(monty.ReplOptions{ScriptName: "<recur>", Limits: montyLimits()})
	if err != nil {
		return err
	}
	capture := &cappedBuffer{}
	var images []Image
	var prefixRun bool
	var metadata map[string]any
	options := monty.FeedOptions{Print: monty.WriterPrintCallback(capture), Functions: map[string]monty.ExternalFunction{
		"docs": docs,
		"__recur_session": func(context.Context, monty.Call) (monty.Result, error) {
			tuple := monty.NamedTuple{TypeName: "Session", FieldNames: []string{"id", "model", "provider", "effort", "cwd", "ctx_size"}}
			for _, key := range tuple.FieldNames {
				value := metadata[key]
				if key == "ctx_size" && value != nil {
					value = int64(value.(float64))
				}
				tuple.Values = append(tuple.Values, monty.MustValueOf(value))
			}
			return monty.Return(monty.MustValueOf(tuple)), nil
		},
		"view":  imageFunction(&images, &prefixRun),
		"read":  fileFunction([]string{"path", "offset", "limit"}, []any{int64(1), int64(200)}, readText),
		"write": fileFunction([]string{"path", "content"}, nil, writeText),
		"patch": fileFunction([]string{"edits"}, nil, patchText),
		"sh": func(_ context.Context, call monty.Call) (monty.Result, error) {
			if len(call.Args) != 1 || len(call.Kwargs) != 0 {
				return pythonError("TypeError", "sh() expects one command string"), nil
			}
			command, ok := call.Args[0].Raw().(string)
			if !ok {
				return pythonError("TypeError", "sh() expects a string"), nil
			}
			// Inherit the worker process group, so parent cancellation kills shell children too.
			cmd := exec.Command("/bin/sh", "-c", command)
			stdout, stderr := &cappedBuffer{}, &cappedBuffer{}
			cmd.Stdout = stdout
			cmd.Stderr = stderr
			err := cmd.Run()
			code := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else {
					return pythonError("OSError", err.Error()), nil
				}
			}
			return monty.Return(monty.MustValueOf(map[string]any{"stdout": stdout.Text(), "stderr": stderr.Text(), "returncode": code})), nil
		},
	}}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		capture.Reset()
		images = nil
		var request struct {
			Code    string
			Prefix  bool
			Session map[string]any
		}
		result := Result{}
		err = json.Unmarshal(scanner.Bytes(), &request)
		if err == nil {
			metadata = request.Session
			_, err = session.FeedRun(context.Background(), "session = __recur_session()", options)
		}
		if err == nil {
			prefixRun = request.Prefix
			source := request.Code
			if !request.Prefix {
				source = displayLastExpression(source)
			}
			_, err = session.FeedRun(context.Background(), source, options)
		}
		if err != nil {
			result.Error = true
			_, _ = fmt.Fprintln(capture, err)
		}
		result.Output = capture.Text()
		result.Images = images
		if err = encoder.Encode(result); err != nil {
			return err
		}
	}
	return scanner.Err()
}
func pythonError(kind, message string) monty.Result {
	return monty.Raise(monty.Exception{Type: kind, Arg: &message})
}

// Monty parses both the original snippet and the candidate expression. The
// lexical scan only locates a final top-level statement; it never validates
// Python syntax or executes code. This preserves syntax-before-side-effects.
func displayLastExpression(source string) string {
	if _, err := monty.New(source, monty.CompileOptions{ScriptName: "<recur>"}); err != nil {
		return source
	}
	start, end := lastStatement(source)
	if start < 0 {
		return source
	}
	expression := strings.TrimSpace(source[start:end])
	probe := "__recur_last_result__ = (\n" + expression + "\n)\n"
	if _, err := monty.New(probe, monty.CompileOptions{ScriptName: "<recur>"}); err != nil {
		return source
	}
	return source[:start] + probe + "if __recur_last_result__ is not None:\n    _ = __recur_last_result__\n    print(repr(__recur_last_result__))\n__recur_last_result__ = None\n"
}
func lastStatement(source string) (int, int) {
	end := len(source)
	start := -1
	depth := 0
	quote := byte(0)
	triple := false
	comment := false
	lineStart := true
	topLine := false
	compound := false
	for i := 0; i < len(source); i++ {
		c := source[i]
		if comment {
			if c == '\n' {
				comment = false
				lineStart = true
			}
			continue
		}
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				if !triple {
					quote = 0
				} else if i+2 < len(source) && source[i+1] == quote && source[i+2] == quote {
					quote = 0
					i += 2
				}
			}
			continue
		}
		if lineStart && depth == 0 {
			lineStart = false
			j := i
			for j < len(source) && (source[j] == ' ' || source[j] == '\t' || source[j] == '\r') {
				j++
			}
			topLine = j == i
			if j < len(source) && source[j] != '\n' && source[j] != '#' && topLine {
				start = i
				end = len(source)
				word := source[i:]
				if end := strings.IndexFunc(word, func(r rune) bool { return !unicode.IsLetter(r) }); end >= 0 {
					word = word[:end]
				}
				compound = false
				switch word {
				case "if", "elif", "else", "for", "while", "try", "except", "finally", "with", "def", "class", "async", "match", "case":
					compound = true
				}
			}
		}
		switch c {
		case '#':
			comment = true
		case '\'', '"':
			quote = c
			triple = i+2 < len(source) && source[i+1] == c && source[i+2] == c
			if triple {
				i += 2
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ';':
			if depth == 0 && topLine && !compound {
				j := i + 1
				for j < len(source) && (source[j] == ' ' || source[j] == '\t') {
					j++
				}
				if j < len(source) && source[j] != '#' && source[j] != '\n' {
					start = j
					end = len(source)
				} else {
					end = i
				}
			}
		case '\\':
			if i+1 < len(source) && source[i+1] == '\n' {
				i++
				lineStart = false
			}
		case '\n':
			lineStart = true
		}
	}
	return start, end
}

// These limits belong to the persistent interpreter. A native MaxDuration would
// count idle time since session creation; per-call timeouts live in REPL.execute.
func montyLimits() *monty.ResourceLimits {
	return &monty.ResourceLimits{MaxMemory: 128 * 1024 * 1024, MaxAllocations: 10_000_000, MaxRecursionDepth: 1000}
}
