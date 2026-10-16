package audit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestTail(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	if es, err := Tail(5); err != nil || es != nil {
		t.Errorf("no log: %v %v", es, err)
	}
	old := tailChunk
	tailChunk = 100 // several chunks, split mid-line
	t.Cleanup(func() { tailChunk = old })
	for i := range 20 {
		Write(Entry{Context: fmt.Sprintf("c%02d", i), Command: []string{"aws", "s3", "ls"}})
	}
	f, _ := os.OpenFile(Path(), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("not json\n") // skipped
	f.Close()
	Write(Entry{Context: "last"})

	es, err := Tail(3)
	var got []string
	for _, e := range es {
		got = append(got, e.Context)
	}
	if err != nil || strings.Join(got, " ") != "c18 c19 last" {
		t.Errorf("tail 3: %v %v", got, err)
	}
	if es, _ := Tail(100); len(es) != 21 || es[0].Context != "c00" { // more than there are
		t.Errorf("tail 100: %d entries, first %+v", len(es), es[0])
	}
	if es, _ := Tail(0); len(es) != 0 {
		t.Errorf("tail 0: %v", es)
	}

	// Just after a rotation, the rest comes from the log before.
	os.Rename(Path(), Path()+".1")
	Write(Entry{Context: "new"})
	es, err = Tail(3)
	got = nil
	for _, e := range es {
		got = append(got, e.Context)
	}
	if err != nil || strings.Join(got, " ") != "c19 last new" {
		t.Errorf("across a rotation: %v %v", got, err)
	}
}

type badLog struct {
	statErr, readErr error
}

func (b badLog) ReadAt([]byte, int64) (int, error) { return 0, b.readErr }
func (badLog) Close() error                        { return nil }
func (b badLog) Stat() (fs.FileInfo, error) {
	if b.statErr != nil {
		return nil, b.statErr
	}
	return os.Stat(os.Args[0]) // any non-empty file
}

func TestTailErrors(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	boom := errors.New("boom")
	old := openLog
	t.Cleanup(func() { openLog = old })
	for _, f := range []func(string) (logFile, error){
		func(string) (logFile, error) { return nil, boom },
		func(string) (logFile, error) { return badLog{statErr: boom}, nil },
		func(string) (logFile, error) { return badLog{readErr: boom}, nil },
	} {
		openLog = f
		if _, err := Tail(1); !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
	}
}
