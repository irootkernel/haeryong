package renderer

import (
	"errors"
	"io"
	"net/http"
	"time"
)

// protectedWriter bounds each socket write and rechecks current authority
// immediately before it. ServeContent's error bodies are held back so an
// error before commitment can replace stale representation headers safely.
type protectedWriter struct {
	http.ResponseWriter
	check   func() error
	status  int
	started bool
	err     error
}

func (w *protectedWriter) WriteHeader(status int) {
	if w.started || w.err != nil {
		return
	}
	w.status = status
	if status >= 400 {
		return
	}
	if err := w.guard(); err != nil {
		return
	}
	w.started = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *protectedWriter) guard() error {
	if err := w.check(); err != nil {
		w.err = err
		return err
	}
	err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.err = err
		return err
	}
	return nil
}

func (w *protectedWriter) Write(value []byte) (int, error) {
	if w.status >= 400 {
		return len(value), nil
	}
	if w.err != nil {
		return 0, w.err
	}
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	if w.err != nil {
		return 0, w.err
	}
	total := 0
	for len(value) > 0 {
		if err := w.guard(); err != nil {
			return total, err
		}
		size := min(len(value), 32*1024)
		n, err := w.ResponseWriter.Write(value[:size])
		total += n
		value = value[n:]
		if err == nil && n != size {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.err = err
			return total, err
		}
	}
	return total, nil
}

func clearContentHeaders(w http.ResponseWriter) {
	for _, name := range []string{"Content-Length", "Content-Type", "Content-Encoding", "ETag", "Haeryong-Derivative", "Haeryong-Content-Digest", "Haeryong-Payload-Digest", "Haeryong-Page-Path", "Haeryong-Resolved-Path"} {
		w.Header().Del(name)
	}
}
