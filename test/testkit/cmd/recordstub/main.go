// recordstub is a fake cloud CLI for tests, on every OS: it appends its name,
// arguments and environment to $STUB_LOG ("name\037arg\037arg...\n", the
// environment one VAR=value per line, then "\036\n"), then prints $STUB_OUT
// and exits with $STUB_EXIT (default 0). A mkdir lock keeps records whole
// when mek runs several stubs at once (mek doctor).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	if log := os.Getenv("STUB_LOG"); log != "" {
		for os.Mkdir(log+".lock", 0o700) != nil {
			time.Sleep(10 * time.Millisecond)
		}
		name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
		rec := strings.Join(append([]string{name}, os.Args[1:]...), "\037") + "\n" +
			strings.Join(os.Environ(), "\n") + "\n\036\n"
		if f, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			f.WriteString(rec)
			f.Close()
		}
		os.Remove(log + ".lock")
	}
	if out := os.Getenv("STUB_OUT"); out != "" {
		fmt.Println(out)
	}
	code, _ := strconv.Atoi(os.Getenv("STUB_EXIT"))
	os.Exit(code)
}
