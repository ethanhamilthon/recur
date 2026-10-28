package agent

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *Agent) listenNotifications() error {
	dir, err := os.MkdirTemp("", "recur-notify-")
	if err != nil {
		return err
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "socket"))
	if err != nil {
		os.RemoveAll(dir)
		return err
	}
	if err = writePrivate(a.HistoryPath+".endpoint", []byte(filepath.Join(dir, "socket"))); err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return err
	}
	a.listener, a.socketDir, a.inbox = listener, dir, make(chan string, 64)
	inbox := a.inbox
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
			var text string
			err = json.NewDecoder(io.LimitReader(connection, 256*1024)).Decode(&text)
			status := ""
			if err != nil {
				status = err.Error()
			} else {
				var escaped bytes.Buffer
				if err = xml.EscapeText(&escaped, []byte(text)); err != nil {
					status = err.Error()
				} else {
					select {
					case inbox <- "<notify>\n" + escaped.String() + "\n</notify>":
					default:
						status = "notification queue is full"
					}
				}
			}
			_ = json.NewEncoder(connection).Encode(status)
			_ = connection.Close()
		}
	}()
	return nil
}
func (a *Agent) Notifications() []string {
	var messages []string
	for {
		select {
		case text := <-a.inbox:
			messages = append(messages, text)
		default:
			return messages
		}
	}
}
func (a *Agent) Inbox() <-chan string { return a.inbox }

// Notify acknowledges enqueueing by the active owner, not acceptance by the model.
func Notify(cwd, id, text string) error {
	if id == "" || filepath.Base(id) != id || strings.TrimSpace(text) == "" || len(text) > 128*1024 {
		return fmt.Errorf("notify requires a session ID and 1..131072 bytes of message text")
	}
	home, err := configDir(cwd)
	if err != nil {
		return err
	}
	directory, err := projectDirectory(cwd, home)
	if err != nil {
		return err
	}
	endpoint, err := os.ReadFile(filepath.Join(directory, id+".jsonl.endpoint"))
	if err != nil {
		return fmt.Errorf("session is inactive: %s", id)
	}
	connection, err := net.DialTimeout("unix", string(endpoint), 2*time.Second)
	if err != nil {
		return fmt.Errorf("session is inactive: %s: %w", id, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if err = json.NewEncoder(connection).Encode(text); err != nil {
		return err
	}
	var status string
	if err = json.NewDecoder(connection).Decode(&status); err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("notify: %s", status)
	}
	return nil
}
