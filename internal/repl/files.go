package repl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	monty "github.com/ewhauser/gomonty"
)

const fileLimit = 4 * 1024 * 1024

var errNotText = errors.New("not supported text")

// bind rejects duplicate, unknown and missing arguments before host side effects.
func bind(call monty.Call, names []string, defaults ...any) ([]any, error) {
	if len(call.Args) > len(names) {
		return nil, fmt.Errorf("too many arguments")
	}
	args, set := make([]any, len(names)), make([]bool, len(names))
	for i, v := range call.Args {
		args[i], set[i] = v.Raw(), true
	}
	for _, pair := range call.Kwargs {
		found := false
		for i, name := range names {
			if pair.Key.Raw() == name {
				if set[i] {
					return nil, fmt.Errorf("duplicate argument %s", name)
				}
				args[i], set[i], found = pair.Value.Raw(), true, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown argument %v", pair.Key.Raw())
		}
	}
	for i := range names {
		if !set[i] {
			d := i - (len(names) - len(defaults))
			if d < 0 {
				return nil, fmt.Errorf("missing argument %s", names[i])
			}
			args[i] = defaults[d]
		}
	}
	return args, nil
}
func fileFunction(names []string, defaults []any, run func([]any) (any, error)) monty.ExternalFunction {
	return func(_ context.Context, call monty.Call) (monty.Result, error) {
		args, err := bind(call, names, defaults...)
		if err != nil {
			return pythonError("TypeError", err.Error()), nil
		}
		result, err := run(args)
		if err != nil {
			return pythonError("ValueError", err.Error()), nil
		}
		value, err := monty.ValueOf(result)
		if err != nil {
			return pythonError("RuntimeError", err.Error()), nil
		}
		return monty.Return(value), nil
	}
}
func textFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	if info.Size() > fileLimit {
		return "", fmt.Errorf("%w: file exceeds 4 MiB: %s", errNotText, path)
	}
	data, err := io.ReadAll(io.LimitReader(f, fileLimit+1))
	if err != nil {
		return "", err
	}
	if len(data) > fileLimit {
		return "", fmt.Errorf("%w: file exceeds 4 MiB: %s", errNotText, path)
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("%w: not UTF-8 text: %s", errNotText, path)
	}
	return string(data), nil
}
func atomicText(path, content string) error {
	if len(content) > fileLimit || !utf8.ValidString(content) {
		return fmt.Errorf("content must be UTF-8 and at most 4 MiB")
	}
	mode := fs.FileMode(0600)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular file: %s", path)
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".recur-edit-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = f.Chmod(mode)
	if err == nil {
		_, err = f.WriteString(content)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}
func clipLine(line string, limit int) string {
	if len(line) <= limit {
		return line
	}
	for limit > 0 && !utf8.RuneStart(line[limit]) {
		limit--
	}
	return line[:limit] + "… [line truncated]"
}
func readText(args []any) (any, error) {
	path, p := args[0].(string)
	offset, o := args[1].(int64)
	limit, l := args[2].(int64)
	if !p || !o || !l || offset == 0 || limit < 1 || limit > 2000 {
		return nil, fmt.Errorf("read(path, offset=1, limit=200): expected string path, non-zero offset, limit 1..2000")
	}
	text, err := textFile(path)
	if err != nil {
		return nil, err
	}
	lines := splitLines(text)
	total := int64(len(lines))
	if offset < 0 {
		offset = max(1, total+offset+1)
	}
	if offset > total {
		if total == 0 {
			return "", nil
		}
		return fmt.Sprintf("[EOF: %d lines]\n", total), nil
	}
	var out strings.Builder
	i := offset - 1
	for ; i < total && i-offset+1 < limit; i++ {
		line := fmt.Sprintf("%d: %s\n", i+1, clipLine(lines[i], 4000))
		if out.Len()+len(line) > 64*1024 {
			fmt.Fprintf(&out, "[truncated at line %d of %d]\n", i+1, total)
			return out.String(), nil
		}
		out.WriteString(line)
	}
	if i < total {
		fmt.Fprintf(&out, "[lines %d-%d of %d]\n", offset, i, total)
	}
	return out.String(), nil
}
func writeText(args []any) (any, error) {
	path, p := args[0].(string)
	text, t := args[1].(string)
	if !p || !t {
		return nil, fmt.Errorf("write(path, content) expects two strings")
	}
	return nil, atomicText(path, text)
}
func patchText(args []any) (any, error) {
	edits, ok := args[0].([]monty.Value)
	if !ok || len(edits) == 0 || len(edits) > 200 {
		return nil, fmt.Errorf("patch(edits) expects a nonempty list of at most 200 {path, old, new} edits")
	}
	original, changed := map[string]string{}, map[string]string{}
	var paths []string
	total := 0
	for _, edit := range edits {
		fields, ok := edit.Raw().(monty.Dict)
		if !ok {
			return nil, fmt.Errorf("each edit must be a dict with path, old, new")
		}
		values := map[string]string{}
		for _, pair := range fields {
			k, key := pair.Key.Raw().(string)
			v, value := pair.Value.Raw().(string)
			if !key || !value || (k != "path" && k != "old" && k != "new") {
				return nil, fmt.Errorf("edit fields must be string path, old, new")
			}
			values[k] = v
		}
		if len(values) != 3 || values["old"] == "" {
			return nil, fmt.Errorf("edit requires path, nonempty old, and new")
		}
		path, err := filepath.Abs(values["path"])
		if err != nil {
			return nil, err
		}
		if _, exists := changed[path]; !exists {
			info, err := os.Lstat(path)
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("patch requires regular files: %s", path)
			}
			text, err := textFile(path)
			if err != nil {
				return nil, err
			}
			total += len(text)
			if total > 16*1024*1024 {
				return nil, fmt.Errorf("patch input exceeds 16 MiB")
			}
			original[path], changed[path] = text, text
			paths = append(paths, path)
		}
		if count := strings.Count(changed[path], values["old"]); count != 1 {
			return nil, fmt.Errorf("old text matched %d times in %s; must match exactly once", count, path)
		}
		changed[path] = strings.Replace(changed[path], values["old"], values["new"], 1)
		if len(changed[path]) > fileLimit {
			return nil, fmt.Errorf("patched file exceeds 4 MiB: %s", path)
		}
	}
	for _, path := range paths {
		current, err := textFile(path)
		if err != nil {
			return nil, err
		}
		if current != original[path] {
			return nil, fmt.Errorf("file changed during patch: %s", path)
		}
	}
	for i, path := range paths {
		if err := atomicText(path, changed[path]); err != nil {
			for _, prior := range paths[:i] {
				err = errors.Join(err, atomicText(prior, original[prior]))
			}
			return nil, err
		}
	}
	return len(paths), nil
}
