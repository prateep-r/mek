package tunnel

import (
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/prateep-r/mek/internal/proc"
)

// handOver gives the supervisor the tunnel's locks and log as fds 3, 4 and
// 5: a flock belongs to the open file, so the supervisor holds it alone
// once this mek closes its copies.
func handOver(cmd *exec.Cmd, life, port, log *os.File) { cmd.ExtraFiles = []*os.File{life, port, log} }

// SuperviseMain is `mek tunnel _supervise`: the locks and log come as fds
// 3-5 from the mek that started it, the spec on in.
func SuperviseMain(in io.Reader, id string) int {
	files := []*os.File{os.NewFile(3, "life.lock"), os.NewFile(4, "port.lock"), os.NewFile(5, "tunnel.log")}
	for _, f := range files {
		syscall.CloseOnExec(int(f.Fd())) // the tunnel's process must not hold the locks
	}
	stop, done := proc.StopRequests(id)
	defer done()
	return Supervise(in, files[0], files[1], files[2], stop)
}

// foregroundStop: `mek tunnel stop` sends a foreground mek SIGTERM,
// which the runner passes on to the tunnel's process.
func foregroundStop(string) (<-chan struct{}, func()) { return nil, func() {} }
