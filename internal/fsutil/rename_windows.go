//go:build windows

package fsutil

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func init() { rename = retryRename }

// renameFor is how long a rename keeps trying.
var renameFor = time.Second

// retryRename: Windows won't replace a file while another process has it
// open (mek tunnel ls reading a record the supervisor is saving) and says
// "access denied" or "sharing violation"; that clears in milliseconds.
func retryRename(from, to string) error {
	deadline := time.Now().Add(renameFor)
	for {
		err := os.Rename(from, to)
		if err == nil || !transient(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func transient(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
