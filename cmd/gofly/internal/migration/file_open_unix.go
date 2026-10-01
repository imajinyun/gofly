//go:build unix

package migration

import (
	"os"
	"syscall"
)

func openSQLFile(root *os.Root, name string) (*os.File, error) {
	// Reject symlinks atomically and avoid blocking on a FIFO swapped in after
	// Lstat. The caller checks the opened descriptor is the same regular file.
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
