package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"

	importer "haeryong.local/haeryong/internal/application/import"
)

type inputReader struct {
	ctx context.Context
	r   io.Reader
}

func (r inputReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}

func (s *Service) transferFile(ctx context.Context, j *Journal, p *Preparation, entry Entry) error {
	input, e := s.State.Open(p.Snapshot, entry)
	if e != nil {
		return e
	}
	defer input.Close()
	hash := sha256.New()
	n, e := io.CopyBuffer(hash, inputReader{ctx, input}, make([]byte, 64*1024))
	if e != nil {
		return e
	}
	if n != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.Hash {
		return importer.ErrInputChanged
	}
	// The ranges are queried as bounded pages twice: verify all retained claims
	// first, then send only gaps. No range count or whole-file memory envelope.
	walk := func(visit func(ConfirmedRange) error) error {
		cursor := ""
		end := int64(0)
		for {
			page, e := s.API.ConfirmedRanges(ctx, p.UploadID, entry.Path, cursor)
			if e != nil {
				return e
			}
			for _, part := range page.Items {
				if part.Offset < end || part.Offset > entry.Size || part.Size > entry.Size-part.Offset {
					return ErrIntent
				}
				if _, e = input.Seek(part.Offset, io.SeekStart); e != nil {
					return e
				}
				segment := sha256.New()
				n, e := io.CopyBuffer(segment, io.LimitReader(inputReader{ctx, input}, part.Size), make([]byte, 64*1024))
				if e != nil {
					return e
				}
				if n != part.Size || hex.EncodeToString(segment.Sum(nil)) != part.Hash {
					return importer.ErrInputChanged
				}
				if visit != nil {
					if e = visit(part); e != nil {
						return e
					}
				}
				end = part.Offset + part.Size
			}
			if page.NextCursor == nil {
				return nil
			}
			if *page.NextCursor == cursor {
				return ErrReconcile
			}
			cursor = *page.NextCursor
		}
	}
	if e = walk(nil); e != nil {
		return e
	}
	sent := false
	sendGap := func(start, end int64) error {
		if _, e := input.Seek(start, io.SeekStart); e != nil {
			return e
		}
		buffer := make([]byte, 1024*1024)
		for start < end {
			size := int64(len(buffer))
			if end-start < size {
				size = end - start
			}
			if _, e := io.ReadFull(inputReader{ctx, input}, buffer[:size]); e != nil {
				return e
			}
			hash := sha256.Sum256(buffer[:size])
			ack, e := s.API.Put(ctx, p.UploadID, importer.Chunk{Path: entry.Path, Kind: "file", Offset: start, Hash: hex.EncodeToString(hash[:]), Length: &entry.Size, Final: start+size == entry.Size}, bytes.NewReader(buffer[:size]))
			if e != nil {
				return e
			}
			p.Checkpoint = ack.Checkpoint
			sent = true
			if e = s.State.Save(*j); e != nil {
				return e
			}
			start += size
		}
		return nil
	}
	position := int64(0)
	var last *ConfirmedRange
	if e = walk(func(part ConfirmedRange) error {
		if e := sendGap(position, part.Offset); e != nil {
			return e
		}
		position = part.Offset + part.Size
		saved := part
		last = &saved
		return nil
	}); e != nil {
		return e
	}
	if e = sendGap(position, entry.Size); e != nil {
		return e
	}
	if !sent || last != nil && last.Offset+last.Size == entry.Size {
		part := ConfirmedRange{Offset: 0, Size: 0, Hash: hex.EncodeToString(sha256.New().Sum(nil))}
		if last != nil {
			part = *last
		}
		if _, e = input.Seek(part.Offset, io.SeekStart); e != nil {
			return e
		}
		// Reaffirm the exact final retained range (or an empty file) end marker.
		ack, e := s.API.Put(ctx, p.UploadID, importer.Chunk{Path: entry.Path, Kind: "file", Offset: part.Offset, Hash: part.Hash, Length: &entry.Size, Final: true}, io.LimitReader(inputReader{ctx, input}, part.Size))
		if e != nil {
			return e
		}
		p.Checkpoint = ack.Checkpoint
		if e = s.State.Save(*j); e != nil {
			return e
		}
	}
	return nil
}
