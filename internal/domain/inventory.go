package domain

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrPackageInvalid = errors.New("PACKAGE_INVALID")

func ValidatePayloadPath(path string) error {
	if !utf8.ValidString(path) || path == "" || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "/") {
		return ErrPackageInvalid
	}
	parts := strings.Split(path, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return ErrPackageInvalid
		}
	}
	if len(parts[0]) >= 2 && ((parts[0][0] >= 'a' && parts[0][0] <= 'z') || (parts[0][0] >= 'A' && parts[0][0] <= 'Z')) && parts[0][1] == ':' {
		return ErrPackageInvalid
	}
	return nil
}

// InventoryLine emits the exact canonical UTF-8 JSONL array, including controls.
func InventoryLine(e Entry) ([]byte, error) {
	if err := ValidatePayloadPath(e.Path); err != nil {
		return nil, err
	}
	var values []string
	switch e.Kind {
	case "file":
		if e.Size < 0 {
			return nil, ErrPackageInvalid
		}
		values = []string{e.Path, "file", strconv.FormatInt(e.Size, 10), e.Hash}
	case "directory":
		values = []string{e.Path, "directory"}
	case "symlink":
		if !utf8.ValidString(e.LinkText) || strings.ContainsRune(e.LinkText, 0) {
			return nil, ErrPackageInvalid
		}
		values = []string{e.Path, "symlink", e.LinkText}
	default:
		return nil, ErrPackageInvalid
	}
	var b bytes.Buffer
	b.WriteByte('[')
	for i, v := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		for _, r := range v {
			switch r {
			case '"', '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 0x20 {
					fmt.Fprintf(&b, `\u%04x`, r)
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte('"')
	}
	b.WriteString("]\n")
	return b.Bytes(), nil
}
