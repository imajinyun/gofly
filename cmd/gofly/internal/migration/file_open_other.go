//go:build !unix

package migration

import (
	"errors"
	"os"
)

func openSQLFile(root *os.Root, name string) (*os.File, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.Join(errors.New("SQL file changed while opening"), err, f.Close())
	}
	return f, nil
}
