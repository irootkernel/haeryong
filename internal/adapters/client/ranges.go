package client

import (
	"context"
	"encoding/hex"
	"net/url"
	"strconv"

	"haeryong.local/haeryong/internal/application/transfer"
)

func (c *Client) ConfirmedRanges(ctx context.Context, id, path, cursor string) (transfer.RangePage, error) {
	var wire struct {
		Items []struct {
			Path   string `json:"path"`
			Hash   string `json:"bytes_sha256"`
			Offset string `json:"offset"`
			Size   string `json:"byte_size"`
		} `json:"items"`
		Next *string `json:"next_cursor"`
	}
	query := url.Values{"path": {path}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	_, e := c.JSON(ctx, "GET", "/api/v1/uploads/"+id+"/ranges?"+query.Encode(), "", nil, &wire)
	if e != nil {
		return transfer.RangePage{}, e
	}
	out := transfer.RangePage{Items: []transfer.ConfirmedRange{}, NextCursor: wire.Next}
	for _, item := range wire.Items {
		offset, e := strconv.ParseInt(item.Offset, 10, 64)
		if e != nil || offset < 0 || strconv.FormatInt(offset, 10) != item.Offset {
			return out, transfer.ErrReconcile
		}
		size, e := strconv.ParseInt(item.Size, 10, 64)
		if e != nil || size < 0 || strconv.FormatInt(size, 10) != item.Size {
			return out, transfer.ErrReconcile
		}
		hash, e := hex.DecodeString(item.Hash)
		if e != nil || len(hash) != 32 || hex.EncodeToString(hash) != item.Hash || item.Path != path {
			return out, transfer.ErrReconcile
		}
		out.Items = append(out.Items, transfer.ConfirmedRange{Path: item.Path, Hash: item.Hash, Offset: offset, Size: size})
	}
	return out, nil
}
