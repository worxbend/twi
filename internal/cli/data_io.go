package cli

import (
	"context"
	"io"
)

// contextWriter allows command cancellation to end a blocked pipe write.
// Writes remain serial: one request is outstanding at most. A writer that ignores
// cancellation may keep that one goroutine until its owner closes the sink.
type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}
type writeResult struct {
	n   int
	err error
}

func (w contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	result := make(chan writeResult, 1)
	// The caller may reuse its buffer after cancellation; retain owned bytes.
	owned := append([]byte(nil), p...)
	go func() {
		n, err := w.writer.Write(owned)
		if n < len(owned) && err == nil {
			err = io.ErrShortWrite
		}
		if err == nil {
			if flush, ok := w.writer.(interface{ Flush() error }); ok {
				err = flush.Flush()
			}
		}
		result <- writeResult{n, err}
	}()
	select {
	case r := <-result:
		return r.n, r.err
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
}
func dataStreams(ctx context.Context, stdout, stderr io.Writer) (io.Writer, io.Writer) {
	return contextWriter{ctx, stdout}, contextWriter{ctx, stderr}
}

// contextReader owns its temporary buffer so cancellation cannot race caller reuse.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	owned := make([]byte, len(p))
	result := make(chan writeResult, 1)
	go func() { n, err := r.reader.Read(owned); result <- writeResult{n, err} }()
	select {
	case v := <-result:
		copy(p, owned[:v.n])
		return v.n, v.err
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}

// trackedWriter preserves output failure precedence for legacy command adapters.
type trackedWriter struct {
	writer io.Writer
	err    error
}

func (w *trackedWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n < len(p) && err == nil {
		err = io.ErrShortWrite
	}
	if err != nil && w.err == nil {
		w.err = err
	}
	return n, err
}
