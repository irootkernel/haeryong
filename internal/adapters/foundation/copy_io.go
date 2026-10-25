package foundation

import (
	"context"
	"io"
)

type contextCopyReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextCopyReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func copyStream(ctx context.Context, w io.Writer, r io.Reader) (int64, error) {
	return io.CopyBuffer(w, contextCopyReader{ctx, r}, make([]byte, 128*1024))
}
