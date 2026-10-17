package client

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type Download struct {
	Path          string `json:"path"`
	Size          string `json:"byte_size"`
	ETag          string `json:"etag"`
	RequestedPath string `json:"requested_path"`
	ResolvedPath  string `json:"resolved_path"`
	SHA256        string `json:"bytes_sha256"`
	Resumed       bool   `json:"resumed"`
}
type partial struct {
	Endpoint, Library, Version, Requested, Resolved, ETag, Hash, Destination string
	Size                                                                     *int64 `json:"byte_size,omitempty"`
}

func digest(header string) (string, error) {
	if !strings.HasPrefix(header, "sha-256=:") || !strings.HasSuffix(header, ":") {
		return "", fmt.Errorf("verified representation digest missing")
	}
	b, e := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(header, "sha-256=:"), ":"))
	if e != nil || len(b) != 32 {
		return "", fmt.Errorf("invalid representation digest")
	}
	return hex.EncodeToString(b), nil
}
func (c *Client) Download(ctx context.Context, version, path, destination string, resolve bool) (Download, json.RawMessage, error) {
	var out Download
	full, e := filepath.Abs(destination)
	if e != nil {
		return out, nil, e
	}
	if _, e = os.Lstat(full); e == nil {
		return out, nil, fmt.Errorf("destination already exists")
	} else if !os.IsNotExist(e) {
		return out, nil, e
	}
	binding := sha256.Sum256([]byte(c.origin + "\x00" + c.credential.LibraryID + "\x00" + version + "\x00" + path + "\x00" + full))
	stem := filepath.Join(filepath.Dir(full), ".haeryong-"+hex.EncodeToString(binding[:])[:24])
	dataPath, metaPath := stem+".partial", stem+".json"
	var saved partial
	offset := int64(0)
	hasMetadata, hasData := false, false
	if info, e := os.Lstat(metaPath); e == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return out, nil, fmt.Errorf("unsafe partial metadata")
		}
		f, e := os.OpenFile(metaPath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			return out, nil, e
		}
		e = json.NewDecoder(f).Decode(&saved)
		f.Close()
		if e != nil {
			return out, nil, e
		}
		hasMetadata = true
		if saved.Endpoint != c.origin || saved.Library != c.credential.LibraryID || saved.Version != version || saved.Requested != path || saved.Destination != full {
			return out, nil, fmt.Errorf("partial output binding mismatch")
		}
	} else if !os.IsNotExist(e) {
		return out, nil, e
	}
	if info, e := os.Lstat(dataPath); e == nil {
		if !hasMetadata || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return out, nil, fmt.Errorf("unsafe partial output")
		}
		offset = info.Size()
		hasData = true
	} else if !os.IsNotExist(e) {
		return out, nil, e
	}
	values := url.Values{"path": {path}}
	if resolve {
		values.Set("resolve_link", "true")
	}
	// A complete verified partial may remain after a real no-replace rename
	// failure. Reauthorize the immutable representation before publishing it.
	endpoint := "/api/v1/versions/" + version + "/file?" + values.Encode()
	complete := false
	if hasMetadata && hasData && (saved.Size == nil || offset >= *saved.Size) {
		head, err := c.Call(ctx, "HEAD", endpoint, "", nil, nil)
		if err != nil {
			return out, nil, err
		}
		head.Body.Close()
		headHash, err := digest(head.Header.Get("Repr-Digest"))
		length, lengthErr := strconv.ParseInt(head.Header.Get("Content-Length"), 10, 64)
		resolved, pathErr := url.QueryUnescape(head.Header.Get("Haeryong-Resolved-Path"))
		if err != nil || lengthErr != nil || pathErr != nil || length < 0 || offset > length || headHash != saved.Hash || head.Header.Get("ETag") != saved.ETag || resolved != saved.Resolved {
			return out, nil, fmt.Errorf("partial output no longer matches immutable representation")
		}
		complete = offset == length
	}
	hash, etag, resolved := saved.Hash, saved.ETag, saved.Resolved
	var body io.Reader = strings.NewReader("")
	var totalSize *int64
	if !complete {
		headers := map[string]string{}
		if offset > 0 {
			headers["Range"] = fmt.Sprintf("bytes=%d-", offset)
			headers["If-Range"] = saved.ETag
		}
		response, err := c.Call(ctx, "GET", endpoint, "", nil, headers)
		if err != nil {
			return out, nil, err
		}
		defer response.Body.Close()
		if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/octet-stream") {
			var value json.RawMessage
			err = json.NewDecoder(response.Body).Decode(&value)
			return out, value, err
		}
		hash, err = digest(response.Header.Get("Repr-Digest"))
		if err != nil {
			return out, nil, err
		}
		etag = response.Header.Get("ETag")
		resolved, err = url.QueryUnescape(response.Header.Get("Haeryong-Resolved-Path"))
		if err != nil {
			return out, nil, err
		}
		if hasMetadata && (etag != saved.ETag || hash != saved.Hash || resolved != saved.Resolved) || offset > 0 && (response.StatusCode != 206 || !strings.HasPrefix(response.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", offset))) {
			return out, nil, fmt.Errorf("partial output no longer matches immutable representation")
		}
		body = response.Body
		if response.ContentLength >= 0 {
			size := offset + response.ContentLength
			totalSize = &size
		}
	}
	if !hasMetadata {
		saved = partial{Endpoint: c.origin, Library: c.credential.LibraryID, Version: version, Requested: path, Resolved: resolved, ETag: etag, Hash: hash, Destination: full, Size: totalSize}
		f, e := os.OpenFile(metaPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			return out, nil, e
		}
		e = json.NewEncoder(f).Encode(saved)
		e = errors.Join(e, f.Sync(), f.Close())
		if e != nil {
			return out, nil, e
		}
		if e = syncDirectory(filepath.Dir(full)); e != nil {
			return out, nil, e
		}
	}
	flags := os.O_RDWR | syscall.O_NOFOLLOW
	if !hasData {
		flags |= os.O_CREATE | os.O_EXCL
	}
	f, e := os.OpenFile(dataPath, flags, 0600)
	if e != nil {
		return out, nil, e
	}
	if _, e = f.Seek(offset, io.SeekStart); e != nil {
		f.Close()
		return out, nil, e
	}
	n, copyError := io.CopyBuffer(f, body, make([]byte, 64*1024))
	e = errors.Join(copyError, f.Sync())
	if e != nil {
		f.Close()
		return out, nil, e
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		f.Close()
		return out, nil, e
	}
	h := sha256.New()
	_, e = io.CopyBuffer(h, f, make([]byte, 64*1024))
	e = errors.Join(e, f.Close())
	if e != nil {
		return out, nil, e
	}
	if hex.EncodeToString(h.Sum(nil)) != hash {
		return out, nil, fmt.Errorf("download integrity mismatch; partial output retained")
	}
	// Native no-replace rename publishes only after full verification.
	// Destination failures remain explicit rather than overwriting unrelated data.
	if e = renameNoReplace(dataPath, full); e != nil {
		return out, nil, e
	}
	dir, e := os.Open(filepath.Dir(full))
	if e != nil {
		return out, nil, e
	}
	e = errors.Join(dir.Sync(), dir.Close())
	if e != nil {
		return out, nil, e
	}

	if e = os.Remove(metaPath); e != nil {
		return out, nil, e
	}
	out = Download{Path: full, Size: fmt.Sprint(offset + n), ETag: etag, RequestedPath: path, ResolvedPath: resolved, SHA256: hash, Resumed: offset > 0}
	return out, nil, nil
}
