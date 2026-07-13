package gateway

import (
	"io"
	"net/http"
)

// flushWriter wraps an http.ResponseWriter and flushes after every write when
// the underlying writer supports it, so streamed output reaches the client
// frame-by-frame instead of being buffered.
type flushWriter struct {
	w  io.Writer
	fl http.Flusher
}

func newFlushWriter(w http.ResponseWriter) *flushWriter {
	fw := &flushWriter{w: w}
	if f, ok := w.(http.Flusher); ok {
		fw.fl = f
	}
	return fw
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.fl != nil {
		fw.fl.Flush()
	}
	return n, err
}
