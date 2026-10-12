//go:build windows

package proc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TryLock takes an exclusive lock on f's first byte without waiting.
func TryLock(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return ErrLocked
	}
	return err
}

// Lock waits for an exclusive lock on f's first byte.
func Lock(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped))
}

// Command runs path with args. A .cmd or .bat (gcloud is gcloud.cmd) runs
// through cmd.exe, which gives ( ) , & | ^ < > a meaning of their own:
// every argument is quoted for it, and one that cmd.exe would expand (%)
// or split (line breaks) is refused: Start returns that error (cmd.Err).
func Command(path string, args ...string) *exec.Cmd {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".cmd" && ext != ".bat" {
		return exec.Command(path, args...)
	}
	line := []string{cmdQuote(path)}
	var bad error
	for _, a := range args {
		if strings.ContainsAny(a, "%\r\n\x00") && bad == nil {
			bad = fmt.Errorf("%s: argument %q has a character cmd.exe would change (%%, a line break)", filepath.Base(path), a)
		}
		line = append(line, cmdQuote(a))
	}
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	cmd := exec.Command(comspec)
	cmd.Args = append([]string{path}, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `/d /s /c "` + strings.Join(line, " ") + `"`}
	cmd.Err = bad
	return cmd
}

// cmdQuote quotes an argument for cmd.exe and then for the program's own
// parser (the C runtime's): inside double quotes cmd.exe leaves its special
// characters alone; a quote is doubled (cmd.exe stays inside the quotes, the
// C runtime reads "" as a quote), and backslashes before a quote or the end
// are doubled.
func cmdQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			slashes++
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, 2*slashes) + `""`)
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
			b.WriteRune(r)
		}
		slashes = 0
	}
	b.WriteString(strings.Repeat(`\`, 2*slashes) + `"`)
	return b.String()
}

// Detach makes cmd outlive this console: no console, its own process group.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
}

type groupSys struct{ job windows.Handle }

func (g *Group) close() {
	if g.sys.job != 0 {
		windows.CloseHandle(g.sys.job)
		g.sys.job = 0
	}
}

// StartGroup starts cmd with no console window (a background tunnel's
// process) in a Job Object of its own.
func StartGroup(cmd *exec.Cmd) (*Group, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
	return startInJob(cmd)
}

// StartAttached starts cmd on this console, in a Job Object that ends it
// (and what it starts) if mek ends — as its process group does on Unix.
func StartAttached(cmd *exec.Cmd) (*Group, error) { return startInJob(cmd) }

func startInJob(cmd *exec.Cmd) (*Group, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	g := &Group{cmd, groupSys{job}}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		g.close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		g.close()
		return nil, err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, h)
		windows.CloseHandle(h)
	}
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		g.close()
		return nil, err
	}
	return g, nil
}

// Terminate ends the group: Windows has no polite stop for a process
// without a console, so it is the same as Kill.
func (g *Group) Terminate() { g.Kill() }

// Kill ends every process in the group now, with exit code 1.
func (g *Group) Kill() {
	if g.sys.job != 0 {
		windows.TerminateJobObject(g.sys.job, 1)
	}
}

// Graceful: Terminate already ended everything.
const Graceful = false

func eventName(id string) *uint16 {
	p, _ := windows.UTF16PtrFromString(`Local\mek-tunnel-` + id) // an id has no NUL
	return p
}

// StopRequests fires when `mek tunnel stop` sets tunnel id's stop event.
func StopRequests(id string) (<-chan struct{}, func()) {
	stop := make(chan struct{})
	ev, err := windows.CreateEvent(nil, 1, 0, eventName(id))
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		err = nil // the same event, with a usable handle
	}
	if err != nil {
		return stop, func() {} // can't be stopped by request; Kill still works
	}
	quit, _ := windows.CreateEvent(nil, 1, 0, nil)
	go func() {
		if r, _ := windows.WaitForMultipleObjects([]windows.Handle{ev, quit}, false, windows.INFINITE); r == windows.WAIT_OBJECT_0 {
			close(stop)
		}
	}()
	return stop, func() {
		windows.SetEvent(quit)
		windows.CloseHandle(ev)
	}
}

// RequestStop sets tunnel id's stop event; pid is unused on Windows.
func RequestStop(id string, _ int) {
	ev, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eventName(id))
	if err != nil {
		return // its owner is gone
	}
	windows.SetEvent(ev)
	windows.CloseHandle(ev)
}

// Kill ends process pid now.
func Kill(pid int) {
	if pid <= 0 {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	windows.TerminateProcess(h, 1)
	windows.CloseHandle(h)
}

// KillGroup: a group's Job Object ends with its owner, so killing the owner
// (Kill) is enough.
func KillGroup(int) {}
