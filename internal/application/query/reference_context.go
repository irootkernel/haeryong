package query

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"

	"golang.org/x/net/html"
	"haeryong.local/haeryong/internal/application/references"
	"haeryong.local/haeryong/internal/application/render"
	"haeryong.local/haeryong/internal/application/search"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type referenceInput struct {
	ctx    context.Context
	reader io.Reader
}

func (r referenceInput) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Static marked placements are inspected outside Store.Write, in verified bytes.
// A dynamic-only mark is unvalidated. Identical placements share a declaration;
// different containing anchor contexts cannot silently choose a destination.
func (s *Service) referencePlacement(ctx context.Context, r domain.AuthoredReference, bound references.EndpointSnapshot) (string, error) {
	var object domain.Object
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if err := bound.Current(tx); err != nil {
			return errReferenceChanged
		}
		var err error
		object, err = tx.Object(bound.Anchor.PageHash)
		return err
	})
	if err != nil {
		return "", err
	}
	if s.Bytes == nil {
		return "", errors.New("reference byte reader unavailable")
	}
	reason := "source_context_mismatch"
	err = s.Bytes.Read(ctx, object, domain.Pin{Kind: "read", OwnerID: r.FromVersionID}, func(input io.ReadSeeker) error {
		reader, err := render.HTMLReader(referenceInput{ctx, input})
		if err != nil {
			return err
		}
		z := html.NewTokenizer(reader)
		stack := [][]string{}
		scope := search.NewElementScope(func(index int) { stack = stack[:index] })
		contexts := map[string]bool{}
		matched := false
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			kind := z.Next()
			if kind == html.ErrorToken {
				if z.Err() != io.EOF {
					return z.Err()
				}
				break
			}
			if kind == html.EndTagToken {
				scope.End(z.Token().Data)
				continue
			}
			if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
				continue
			}
			t := z.Token()
			scope.Start(t.Data)
			elementIDs := []string{}
			attrs := map[string]string{}
			for _, a := range t.Attr {
				if _, ok := attrs[a.Key]; !ok {
					attrs[a.Key] = a.Val
				}
			}
			for _, name := range []string{"id", "data-haeryong-region"} {
				if id := attrs[name]; id != "" && !includes(elementIDs, id) {
					elementIDs = append(elementIDs, id)
				}
			}
			if attrs["data-haeryong-reference"] == r.ID {
				ids := []string{}
				for _, parent := range stack {
					ids = append(ids, parent...)
				}
				ids = append(ids, elementIDs...)
				region := bound.Region
				contextIDs := ids
				if region == nil || includes(ids, *region) {
					matched = true
					// Explicit page/region provenance is the context. Distinct
					// child element IDs inside it do not create new contexts.
					if region != nil {
						contextIDs = []string{*region}
					}
				}
				raw, _ := json.Marshal(contextIDs)
				contexts[string(raw)] = true
			}
			if scope.Push(t.Data, len(stack), kind == html.SelfClosingTagToken) {
				stack = append(stack, elementIDs)
			}
		}
		if len(contexts) > 1 {
			reason = "ambiguous_reference_context"
		} else if matched {
			reason = ""
		}
		return nil
	})
	return reason, err
}

// A bound click proves source context before performing any target lookup.
func (s *Service) boundReference(ctx context.Context, r domain.AuthoredReference, o ReferenceOptions) (*references.EndpointSnapshot, *references.EndpointSnapshot, string, error) {
	src, err := references.Inspect(ctx, s.Store, r.FromVersionID, r.FromPagePath, r.FromRegionID)
	if err != nil {
		return nil, nil, "", err
	}
	bound, err := references.Inspect(ctx, s.Store, r.FromVersionID, o.Page, o.Region)
	if err != nil {
		return &src, nil, "", err
	}
	reason := src.Reason
	if bound.Reason != "" {
		reason = bound.Reason
	} else if r.FromPagePath != nil && (!reflect.DeepEqual(src.Page, bound.Page) || (r.FromRegionID != nil && !reflect.DeepEqual(r.FromRegionID, o.Region))) {
		reason = "source_context_mismatch"
	} else {
		placement, e := s.referencePlacement(ctx, r, bound)
		if e != nil {
			return &src, &bound, "", e
		}
		if placement != "" {
			reason = placement
		}
	}
	return &src, &bound, reason, nil
}
