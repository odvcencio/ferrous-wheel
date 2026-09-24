package ops

import (
	"errors"
	"io"
	"sync"
)

type ioFailures struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	list    []error
}

func (f *ioFailures) add(err error) {
	if err == nil || errors.Is(err, io.EOF) {
		return
	}
	f.mu.Lock()
	f.list = append(f.list, err)
	f.mu.Unlock()
}

func (f *ioFailures) errors() []error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]error(nil), f.list...)
}

type checkedReader struct {
	reader   io.Reader
	failures *ioFailures
}

func (r checkedReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.failures.add(err)
	return n, err
}

type checkedWriter struct {
	writer   io.Writer
	failures *ioFailures
	writeMu  *sync.Mutex
}

func (w checkedWriter) Write(p []byte) (int, error) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	n, err := w.writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.failures.add(err)
	return n, err
}
