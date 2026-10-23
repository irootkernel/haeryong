package references

import (
	"context"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// EndpointError is a structural diagnostic, never an eligibility decision.
type EndpointError struct{ Reason string }

func (e *EndpointError) Error() string { return e.Reason }
func (e *EndpointError) Unwrap() error { return errUnavailable }

func anchorReason(reason string) string {
	switch reason {
	case "missing_region":
		return "anchor_missing"
	case "ambiguous_region":
		return "anchor_ambiguous"
	case "anchor_drift":
		return reason
	default:
		return "anchor_pending"
	}
}

// EndpointSnapshot reuses maintenance's exact-first inventory and anchor rules.
// Inspect only reads; callers must establish eligibility before inspecting, and
// rejoin detached work through Current before releasing a projection.
type EndpointSnapshot struct {
	Version      domain.Version
	Page, Region *string
	Anchor       domain.ReferenceAnchor
	Reason       string
	endpoint     endpoint
}

func Inspect(ctx context.Context, store app.Store, version string, page, region *string) (EndpointSnapshot, error) {
	e, err := (&Worker{Store: store}).endpoint(ctx, version, page, region)
	out := EndpointSnapshot{Version: e.version, Page: e.page, Region: e.region, Anchor: e.anchor, endpoint: e}
	var diagnostic *EndpointError
	if errors.As(err, &diagnostic) {
		out.Reason = diagnostic.Reason
		return out, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		out.Reason = "page_missing"
		return out, nil
	}
	return out, err
}

func (e EndpointSnapshot) Current(tx app.WriteTx) error { return e.endpoint.matches(tx) }
