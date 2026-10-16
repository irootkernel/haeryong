// Package staging expands a verified, pinned archive without retaining its whole
// directory in memory. Durable bytes and metadata remain owned by the caller.
package staging

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	importer "haeryong.local/haeryong/internal/application/import"
)

type Codec struct{}

func New() *Codec { return &Codec{} }

// Expand walks one central-directory record at a time. Each projected ZIP has
// exactly one record, so archive/zip supplies decompression and descriptor
// checks without allocating a slice proportional to the archive's entry count.
// emit must synchronously consume the file reader before returning success.
func (*Codec) Expand(ctx context.Context, source io.ReaderAt, size int64, emit func(importer.EntryInput, io.Reader) error) (result error) {
	defer func() {
		var corrupt flate.CorruptInputError
		if errors.Is(result, zip.ErrFormat) || errors.Is(result, zip.ErrChecksum) || errors.Is(result, zip.ErrAlgorithm) || errors.Is(result, io.ErrUnexpectedEOF) || errors.As(result, &corrupt) {
			result = errors.Join(importer.ErrPackageInvalid, result)
		}
	}()
	if source == nil || emit == nil || size < 0 {
		return zip.ErrFormat
	}
	d, err := directory(source, size)
	if err != nil {
		return err
	}
	offset := d.offset
	for count := uint64(0); count < d.count; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var header [46]byte
		if offset > d.end-46 {
			return zip.ErrFormat
		}
		if _, err := source.ReadAt(header[:], offset); err != nil {
			return err
		}
		if le.Uint32(header[:]) != 0x02014b50 {
			return zip.ErrFormat
		}
		length := int64(46) + int64(le.Uint16(header[28:])) + int64(le.Uint16(header[30:])) + int64(le.Uint16(header[32:]))
		if length > d.end-offset || le.Uint16(header[34:]) != 0 {
			return zip.ErrFormat
		}
		record := make([]byte, int(length)) // ZIP's three uint16 fields bound one record.
		if _, err := source.ReadAt(record, offset); err != nil {
			return err
		}
		offset += length
		prefix := size - d.base
		tail := singleDirectory(record, prefix)
		if prefix > math.MaxInt64-int64(len(tail)) {
			return zip.ErrFormat
		}
		projected := &projection{source: source, base: d.base, prefix: prefix, tail: tail}
		reader, err := zip.NewReader(projected, prefix+int64(len(tail)))
		if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
			return err
		}
		if reader == nil || len(reader.File) != 1 {
			return zip.ErrFormat
		}
		file := reader.File[0]
		path, err := entryName(file)
		if err != nil {
			return err
		}
		kind := "file"
		if file.Mode().IsDir() {
			kind = "directory"
			path = strings.TrimSuffix(path, "/")
		}
		if file.Mode()&os.ModeSymlink != 0 {
			kind = "symlink"
		}
		if file.Mode().Type() != 0 && kind == "file" {
			return importer.PathError(path, importer.ErrPackageInvalid)
		}
		if err := importer.ValidatePath(path); err != nil {
			return err
		}
		start, err := file.DataOffset()
		if err != nil || start < 0 || start > d.offset-d.base || file.CompressedSize64 > uint64(d.offset-d.base-start) || file.UncompressedSize64 > math.MaxInt64 {
			return zip.ErrFormat
		}
		input := importer.EntryInput{Path: path, Kind: kind, Size: int64(file.UncompressedSize64)}
		// archive/zip treats a trailing slash as an unreadable directory. The
		// inventory kind is already captured; open its compressed stream as an
		// ordinary empty file so corrupt directory data cannot bypass verification.
		if kind == "directory" {
			file.Name = strings.TrimSuffix(file.Name, "/")
		}
		body, err := file.Open()
		if err != nil {
			return err
		}
		checked := &checkedReader{ctx: ctx, reader: body, crc: crc32.NewIEEE()}
		if kind == "symlink" {
			text, readErr := io.ReadAll(checked)
			if readErr != nil {
				body.Close()
				return readErr
			}
			if !utf8.Valid(text) || bytes.IndexByte(text, 0) >= 0 {
				body.Close()
				return importer.PathError(path, importer.ErrPackageInvalid)
			}
			input.LinkTarget = string(text)
		} else if kind == "directory" {
			if input.Size != 0 {
				body.Close()
				return zip.ErrFormat
			}
		}
		var entryErr error
		if kind == "file" {
			entryErr = emit(input, checked)
		}
		if entryErr == nil {
			var extra [1]byte
			n, readErr := checked.Read(extra[:])
			if n != 0 || readErr != io.EOF {
				entryErr = errors.Join(errors.New("ZIP entry was not completely consumed"), readErr)
			}
		}
		if entryErr == nil && (checked.size != file.UncompressedSize64 || checked.crc.Sum32() != file.CRC32) {
			entryErr = zip.ErrChecksum
		}
		entryErr = errors.Join(entryErr, body.Close())
		if entryErr != nil {
			return fmt.Errorf("ZIP entry %q: %w", path, entryErr)
		}
		if kind != "file" {
			if err := emit(input, nil); err != nil {
				return err
			}
		}
	}
	if offset != d.end {
		return zip.ErrFormat
	}
	return nil
}

var le = binary.LittleEndian

type archiveDirectory struct {
	offset, end, base int64
	count             uint64
}

func directory(r io.ReaderAt, size int64) (archiveDirectory, error) {
	var out archiveDirectory
	if size < 22 {
		return out, zip.ErrFormat
	}
	length := min(size, int64(22+65535))
	buffer := make([]byte, int(length))
	if _, err := r.ReadAt(buffer, size-length); err != nil {
		return out, err
	}
	p := -1
	for i := len(buffer) - 22; i >= 0; i-- {
		if le.Uint32(buffer[i:]) == 0x06054b50 && i+22+int(le.Uint16(buffer[i+20:])) == len(buffer) {
			p = i
			break
		}
	}
	if p < 0 {
		return out, zip.ErrFormat
	}
	h := buffer[p:]
	if le.Uint16(h[4:]) != 0 || le.Uint16(h[6:]) != 0 || le.Uint16(h[8:]) != le.Uint16(h[10:]) {
		return out, zip.ErrFormat
	}
	count, dirSize, dirOffset := uint64(le.Uint16(h[10:])), uint64(le.Uint32(h[12:])), uint64(le.Uint32(h[16:]))
	end := size - length + int64(p)
	if count == 65535 || dirSize == math.MaxUint32 || dirOffset == math.MaxUint32 {
		var locator [20]byte
		if end < 20 {
			return out, zip.ErrFormat
		}
		if _, err := r.ReadAt(locator[:], end-20); err != nil {
			return out, err
		}
		if le.Uint32(locator[:]) != 0x07064b50 {
			// Exactly 65,535 records still fit a classic directory. A missing
			// ZIP64 locator is valid when size and offset need no extension.
			if dirSize == math.MaxUint32 || dirOffset == math.MaxUint32 {
				return out, zip.ErrFormat
			}
		} else {
			if le.Uint32(locator[4:]) != 0 || le.Uint32(locator[16:]) != 1 {
				return out, zip.ErrFormat
			}
			position := le.Uint64(locator[8:])
			if end < 76 || position > uint64(end-76) || position > math.MaxInt64 {
				return out, zip.ErrFormat
			}
			var z [56]byte
			if _, err := r.ReadAt(z[:], int64(position)); err != nil {
				return out, err
			}
			if le.Uint32(z[:]) != 0x06064b50 || le.Uint64(z[4:]) < 44 || le.Uint64(z[4:]) > uint64(end-20)-position-12 || le.Uint32(z[16:]) != 0 || le.Uint32(z[20:]) != 0 || le.Uint64(z[24:]) != le.Uint64(z[32:]) {
				return out, zip.ErrFormat
			}
			count, dirSize, dirOffset = le.Uint64(z[32:]), le.Uint64(z[40:]), le.Uint64(z[48:])
			end = int64(position)
		}
	}
	if dirSize > uint64(end) || dirOffset > math.MaxInt64 {
		return out, zip.ErrFormat
	}
	base := end - int64(dirSize) - int64(dirOffset)
	if base < 0 {
		return out, zip.ErrFormat
	}
	// Some producers record absolute offsets even with an executable prefix.
	if base > 0 && dirOffset+4 <= uint64(size) {
		var sig [4]byte
		if _, err := r.ReadAt(sig[:], int64(dirOffset)); err == nil && le.Uint32(sig[:]) == 0x02014b50 {
			base = 0
		}
	}
	offset := base + int64(dirOffset)
	if offset < 0 || offset > end || int64(dirSize) > end-offset {
		return out, zip.ErrFormat
	}
	return archiveDirectory{offset: offset, end: offset + int64(dirSize), base: base, count: count}, nil
}

// singleDirectory appends ZIP64 end records even for small archives. That keeps
// offset handling identical when the verified spool exceeds four GiB.
func singleDirectory(record []byte, prefix int64) []byte {
	out := make([]byte, len(record)+56+20+22)
	copy(out, record)
	z := out[len(record):]
	le.PutUint32(z, 0x06064b50)
	le.PutUint64(z[4:], 44)
	le.PutUint16(z[12:], 45)
	le.PutUint16(z[14:], 45)
	le.PutUint64(z[24:], 1)
	le.PutUint64(z[32:], 1)
	le.PutUint64(z[40:], uint64(len(record)))
	le.PutUint64(z[48:], uint64(prefix))
	l := z[56:]
	le.PutUint32(l, 0x07064b50)
	le.PutUint64(l[8:], uint64(prefix)+uint64(len(record)))
	le.PutUint32(l[16:], 1)
	e := l[20:]
	le.PutUint32(e, 0x06054b50)
	le.PutUint16(e[8:], 65535)
	le.PutUint16(e[10:], 65535)
	le.PutUint32(e[12:], math.MaxUint32)
	le.PutUint32(e[16:], math.MaxUint32)
	return out
}

type projection struct {
	source       io.ReaderAt
	base, prefix int64
	tail         []byte
}

func (p *projection) ReadAt(b []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, zip.ErrFormat
	}
	n := 0
	if offset < p.prefix {
		length := min(int64(len(b)), p.prefix-offset)
		read, err := p.source.ReadAt(b[:int(length)], p.base+offset)
		n += read
		offset += int64(read)
		b = b[read:]
		if err != nil {
			return n, err
		}
	}
	if len(b) > 0 {
		index := offset - p.prefix
		if index < 0 || index >= int64(len(p.tail)) {
			return n, io.EOF
		}
		read := copy(b, p.tail[int(index):])
		n += read
		if read < len(b) {
			return n, io.EOF
		}
	}
	return n, nil
}

type checkedReader struct {
	ctx    context.Context
	reader io.Reader
	crc    interface {
		Write([]byte) (int, error)
		Sum32() uint32
	}
	size uint64
}

func (r *checkedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	r.size += uint64(n)
	_, _ = r.crc.Write(p[:n])
	return n, err
}

func entryName(f *zip.File) (string, error) {
	if f.Flags&0x800 != 0 {
		if !utf8.ValidString(f.Name) {
			return "", zip.ErrFormat
		}
		return f.Name, nil
	}
	var unicodeName *string
	for rest := f.Extra; len(rest) >= 4; {
		tag, length := le.Uint16(rest), int(le.Uint16(rest[2:]))
		rest = rest[4:]
		if length > len(rest) {
			return "", zip.ErrFormat
		}
		field := rest[:length]
		rest = rest[length:]
		if tag == 0x7075 {
			if len(field) < 5 || field[0] != 1 || le.Uint32(field[1:]) != crc32.ChecksumIEEE([]byte(f.Name)) || !utf8.Valid(field[5:]) {
				return "", zip.ErrFormat
			}
			name := string(field[5:])
			if unicodeName != nil && *unicodeName != name {
				return "", zip.ErrFormat
			}
			unicodeName = &name
		}
	}
	if unicodeName != nil {
		return *unicodeName, nil
	}
	var name strings.Builder
	for _, b := range []byte(f.Name) {
		if b < 128 {
			name.WriteByte(b)
		} else {
			name.WriteRune(cp437[int(b)-128])
		}
	}
	return name.String(), nil
}

var cp437 = []rune("ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥₧ƒáíóúñÑªº¿⌐¬½¼¡«»░▒▓│┤╡╢╖╕╣║╗╝╜╛┐└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■ ")
