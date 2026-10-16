package selfupdate

import "github.com/prateep-r/mek/internal/fsutil"

// The release archive and the binary in it (see .goreleaser.yaml).
const (
	archiveExt = ".tar.gz"
	binaryName = "mek"
)

func extract(archive []byte, name string) ([]byte, error) { return extractTGZ(archive, name) }

// replace swaps the binary in one rename: a running mek keeps its old file.
func replace(exe string, bin []byte) error { return fsutil.WriteFileAtomic(exe, bin, 0o755) }
