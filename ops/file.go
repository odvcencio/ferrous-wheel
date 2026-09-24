package ops

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// AppendFile writes data to the end of a file and reports write and close
// errors. It creates the file with perm if it does not exist. Like os.OpenFile,
// it follows symlinks; callers must keep the parent directory trusted.
func AppendFile(path string, data []byte, perm fs.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("ops: open append file %q: %w", path, err)
	}
	n, writeErr := file.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("ops: append file %q: %w", path, err)
	}
	return nil
}
