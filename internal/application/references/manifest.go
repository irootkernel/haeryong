package references

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/jsonstream"
	"haeryong.local/haeryong/internal/application/referencecodec"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"time"
)

// Member distinguishes a top-level member boundary from an array item boundary.
// Old checkpoints at zero still start at the object; positive old offsets remain array offsets.
type manifestPosition struct {
	Offset int64 `json:"offset"`
	Member bool  `json:"member,omitempty"`
}

func nonspace(r *bufio.Reader) (byte, error) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if b != ' ' && b != '\n' && b != '\r' && b != '\t' {
			return b, nil
		}
	}
}

// referenceStart resumes only at top-level member boundaries. The importer and
// this scan share one streaming value grammar, including large authored strings.
func referenceStart(ctx context.Context, r io.ReadSeeker, pos manifestPosition, deadline time.Time) (manifestPosition, bool, error) {
	if _, err := r.Seek(pos.Offset, io.SeekStart); err != nil {
		return pos, false, err
	}
	reader := bufio.NewReader(r)
	position := func() int64 { n, _ := r.Seek(0, io.SeekCurrent); return n - int64(reader.Buffered()) }
	c, err := nonspace(reader)
	if err != nil {
		return pos, false, err
	}
	if pos.Offset == 0 {
		if c != '{' {
			return pos, false, domain.ErrContentIntegrity
		}
	} else {
		if c == '}' {
			return manifestPosition{Offset: position()}, true, nil
		}
		if c != ',' {
			return pos, false, domain.ErrContentIntegrity
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return pos, false, err
		}
		c, err = nonspace(reader)
		if err != nil {
			return pos, false, err
		}
		if c == '}' {
			return manifestPosition{Offset: position()}, true, nil
		}
		if err = reader.UnreadByte(); err != nil {
			return pos, false, err
		}
		var key bytes.Buffer
		if err = jsonstream.CopyJSONValue(reader, &key); err != nil {
			return pos, false, err
		}
		var name string
		if err = json.Unmarshal(key.Bytes(), &name); err != nil {
			return pos, false, err
		}
		c, err = nonspace(reader)
		if err != nil {
			return pos, false, err
		}
		if c != ':' {
			return pos, false, domain.ErrContentIntegrity
		}
		if name == "references" {
			c, err = nonspace(reader)
			if err != nil {
				return pos, false, err
			}
			if c != '[' {
				return pos, false, domain.ErrContentIntegrity
			}
			return manifestPosition{Offset: position()}, false, nil
		}
		if err = jsonstream.CopyJSONValue(reader, io.Discard); err != nil {
			return pos, false, err
		}
		pos = manifestPosition{Offset: position(), Member: true}
		if time.Now().After(deadline) {
			return pos, false, nil
		}
		c, err = nonspace(reader)
		if err != nil {
			return pos, false, err
		}
		if c == '}' {
			return manifestPosition{Offset: position()}, true, nil
		}
		if c != ',' {
			return pos, false, domain.ErrContentIntegrity
		}
	}
}

// manifestBatch checkpoints byte offsets in the verified immutable object, so a
// resumed turn never rescans earlier declarations. Limits bound work, not input.
type manifestItem struct {
	prepared app.PreparedReference
	offset   int64
}

func manifestBatch(ctx context.Context, r io.ReadSeeker, version string, pos manifestPosition) (items []manifestItem, next manifestPosition, done bool, err error) {
	deadline := time.Now().Add(8 * time.Millisecond)
	defer func() {
		if errors.Is(err, jsonstream.ErrInvalid) {
			err = domain.ErrContentIntegrity
		}
	}()
	next = pos
	if pos.Offset == 0 || pos.Member {
		next, done, err = referenceStart(ctx, r, pos, deadline)
		if err != nil || done || next.Member {
			return
		}
	}
	for len(items) < 64 {
		if err = ctx.Err(); err != nil {
			return
		}
		if _, err = r.Seek(next.Offset, io.SeekStart); err != nil {
			return
		}
		reader := bufio.NewReader(r)
		var c byte
		for {
			c, err = reader.ReadByte()
			if err != nil {
				return
			}
			next.Offset++
			if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
				break
			}
		}
		if c == ']' {
			done = true
			return
		}
		if c == ',' {
			// Decoder consumes whitespace before the next value.
		} else {
			if err = reader.UnreadByte(); err != nil {
				return
			}
			next.Offset--
		}
		d := json.NewDecoder(reader)
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return
		}
		next.Offset += d.InputOffset()
		var declaration domain.AuthoredReference
		declaration, err = referencecodec.DecodeReference(raw)
		if err != nil {
			return
		}
		declaration.FromVersionID = version
		var prepared app.PreparedReference
		prepared, err = app.PrepareAuthoredReference(declaration)
		if err != nil {
			return
		}
		items = append(items, manifestItem{prepared, next.Offset})
		if time.Now().After(deadline) {
			return
		}
	}
	return
}

var errUnavailable = errors.New("reference structural context unavailable")
