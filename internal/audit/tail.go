package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
)

// logFile is the part of *os.File Tail uses.
type logFile interface {
	io.ReaderAt
	io.Closer
	Stat() (fs.FileInfo, error)
}

// Test seams: tailChunk is how much of the log Tail reads at a time.
var (
	tailChunk int64 = 64 << 10
	openLog         = func(path string) (logFile, error) { return os.Open(path) }
)

// Tail returns the last n entries of the audit log, oldest first. It reads
// the file backwards, so a large log costs only its last lines; lines that
// don't parse are skipped. No log is no entries.
func Tail(n int) ([]Entry, error) {
	f, err := openLog(Path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	end := fi.Size()
	var buf []byte // the file's last bytes
	pos := end
	for {
		out := lastEntries(buf, n, pos > 0)
		if len(out) >= n || pos == 0 {
			return reversed(out), nil
		}
		size := min(tailChunk, pos)
		pos -= size
		chunk := make([]byte, size)
		if _, err := f.ReadAt(chunk, pos); err != nil {
			return nil, err
		}
		buf = append(chunk, buf...)
	}
}

// lastEntries parses up to n entries from the end of buf, newest first.
// partial: buf starts mid-line, so its first line is left out.
func lastEntries(buf []byte, n int, partial bool) []Entry {
	lines := bytes.Split(bytes.TrimRight(buf, "\n"), []byte("\n"))
	if partial {
		lines = lines[1:]
	}
	var out []Entry
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		var e Entry
		if json.Unmarshal(lines[i], &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

func reversed(out []Entry) []Entry {
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
