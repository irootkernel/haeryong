package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

var errInputFormat = errors.New("provider requires UTF-8 text input")

type capturedBytes interface {
	Read(context.Context, domain.Object, domain.Pin, func(io.ReadSeeker) error) error
}
type guardedReader struct {
	reader io.Reader
	guard  func() error
}

func (r guardedReader) Read(p []byte) (int, error) {
	if e := r.guard(); e != nil {
		return 0, e
	}
	return r.reader.Read(p)
}

// SemanticRequestSHA excludes operation/job/capture IDs and observation time.
// It is calculated at trusted admission from the actual prompt and exact inputs.
func SemanticRequestSHA(principal string, target json.RawMessage, invocation domain.ProviderInvocation, reads []domain.CapturedRead) (string, error) {
	stable := []semanticInput{}
	for _, r := range reads {
		item, e := semanticIdentity(r)
		if e != nil {
			return "", e
		}
		stable = append(stable, item)
	}
	sort.Slice(stable, func(i, j int) bool { return semanticKey(stable[i]) < semanticKey(stable[j]) })
	raw, e := json.Marshal(struct {
		Principal  string
		Target     json.RawMessage
		Provider   string
		Invocation domain.ProviderInvocation
		Inputs     []semanticInput
	}{principal, target, "codex", invocation, stable})
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type semanticInput struct {
	Source   json.RawMessage `json:"source"`
	Location json.RawMessage `json:"location"`
	Hash     string          `json:"hash"`
}

func semanticIdentity(r domain.CapturedRead) (semanticInput, error) {
	var source map[string]json.RawMessage
	if json.Unmarshal(r.SourceJSON, &source) != nil {
		return semanticInput{}, domain.ErrInvalidTarget
	}
	delete(source, "snapshot_id")
	raw, e := json.Marshal(source)
	if e != nil {
		return semanticInput{}, e
	}
	return semanticInput{Source: raw, Location: r.LocationJSON, Hash: r.BytesSHA}, nil
}
func semanticKey(item semanticInput) string { raw, _ := json.Marshal(item); return string(raw) }

// Both the semantic identity and actual disclosure frame use this exact order;
// random local read/capture IDs can never reorder the provider's input.
func (d *Driver) orderedInputs(ctx context.Context, a domain.ProviderAttempt, inputs []domain.ProviderInput) ([]domain.ProviderInput, error) {
	type ordered struct {
		input domain.ProviderInput
		key   string
	}
	entries := []ordered{}
	e := d.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.GuardProviderPayload(a.JobID, a.ID, false); e != nil {
			return e
		}
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		var scope domain.ProviderScope
		if json.Unmarshal(j.Scope, &scope) != nil || len(scope.ReadIDs) != len(inputs) {
			return domain.ErrInvalidTarget
		}
		wanted := map[string]bool{}
		seen := map[string]bool{}
		for _, id := range scope.ReadIDs {
			wanted[id] = true
		}
		for _, input := range inputs {
			if !wanted[input.ReadID] || seen[input.ReadID] {
				return domain.ErrInvalidTarget
			}
			seen[input.ReadID] = true
			read, e := tx.CapturedRead(input.ReadID)
			if e != nil {
				return e
			}
			if read.BytesSHA != input.Hash || read.TargetSetID != a.TargetSetID {
				return domain.ErrInvalidTarget
			}
			identity, e := semanticIdentity(read)
			if e != nil {
				return e
			}
			entries = append(entries, ordered{input, semanticKey(identity)})
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	result := make([]domain.ProviderInput, len(entries))
	for i, entry := range entries {
		result[i] = entry.input
	}
	return result, nil
}

func (d *Driver) guard(ctx context.Context, a domain.ProviderAttempt, reconcile bool) error {
	return d.Store.Write(ctx, func(tx app.WriteTx) error { return tx.GuardProviderPayload(a.JobID, a.ID, reconcile) })
}
func (d *Driver) invocation(ctx context.Context, a domain.ProviderAttempt) (domain.ProviderInvocation, error) {
	var scope domain.ProviderScope
	var reads []domain.CapturedRead
	e := d.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.GuardProviderPayload(a.JobID, a.ID, false); e != nil {
			return e
		}
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		if json.Unmarshal(j.Scope, &scope) != nil || scope.ProviderID != "codex" || scope.Invocation == nil {
			return domain.ErrInvalidTarget
		}
		for _, id := range scope.ReadIDs {
			r, e := tx.CapturedRead(id)
			if e != nil {
				return e
			}
			reads = append(reads, r)
		}
		return nil
	})
	if e != nil {
		return domain.ProviderInvocation{}, e
	}
	calculated, e := SemanticRequestSHA(scope.PrincipalID, scope.Target, *scope.Invocation, reads)
	if e != nil || calculated != a.RequestSHA {
		return domain.ProviderInvocation{}, domain.ErrInvalidTarget
	}
	return *scope.Invocation, nil
}

func quoted(w io.Writer, r io.Reader) error {
	if _, e := io.WriteString(w, `"`); e != nil {
		return e
	}
	reader := bufio.NewReader(r)
	for {
		value, size, e := reader.ReadRune()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if value == utf8.RuneError && size == 1 {
			return errInputFormat
		}
		encoded, _ := json.Marshal(string(value))
		if _, e = w.Write(encoded[1 : len(encoded)-1]); e != nil {
			return e
		}
	}
	_, e := io.WriteString(w, `"`)
	return e
}
func (d *Driver) frame(ctx context.Context, a domain.ProviderAttempt, inputs []domain.ProviderInput, inv domain.ProviderInvocation, threadID string) (*os.File, string, error) {
	f, e := os.CreateTemp(d.Scratch, "provider-input-*")
	if e != nil {
		return nil, "", e
	}
	fail := func(e error) (*os.File, string, error) {
		name := f.Name()
		f.Close()
		os.Remove(name)
		return nil, "", e
	}
	hash := sha256.New()
	buffer := bufio.NewWriterSize(io.MultiWriter(f, hash), 65536)
	w := io.Writer(buffer)
	thread, _ := json.Marshal(threadID)
	if _, e = fmt.Fprintf(w, `{"threadId":%s,"input":[{"type":"text","text":`, thread); e != nil {
		return fail(e)
	}
	if e = quoted(w, strings.NewReader(inv.Prompt)); e != nil {
		return fail(e)
	}
	if _, e = io.WriteString(w, `}`); e != nil {
		return fail(e)
	}
	for _, v := range inputs {
		var object domain.Object
		var read domain.CapturedRead
		e = d.Store.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.GuardProviderPayload(a.JobID, a.ID, false); e != nil {
				return e
			}
			var e error
			read, e = tx.CapturedRead(v.ReadID)
			if e != nil {
				return e
			}
			if read.BytesSHA != v.Hash || read.TargetSetID != a.TargetSetID {
				return domain.ErrInvalidTarget
			}
			object, e = tx.Object(v.Hash)
			return e
		})
		if e != nil {
			return fail(e)
		}
		identity, e := semanticIdentity(read)
		if e != nil {
			return fail(e)
		}
		header, _ := json.Marshal(struct {
			Source   json.RawMessage `json:"source"`
			Location json.RawMessage `json:"location"`
			SHA      string          `json:"bytes_sha256"`
		}{identity.Source, identity.Location, v.Hash})
		if _, e = io.WriteString(w, `,{"type":"text","text":`); e != nil {
			return fail(e)
		}
		if e = quoted(w, strings.NewReader(string(header))); e != nil {
			return fail(e)
		}
		if _, e = io.WriteString(w, `},{"type":"text","text":`); e != nil {
			return fail(e)
		}
		e = d.Bytes.Read(ctx, object, domain.Pin{Kind: "provider_continuation", OwnerID: a.ID}, func(r io.ReadSeeker) error {
			return quoted(w, guardedReader{r, func() error { return d.guard(ctx, a, false) }})
		})
		if e != nil {
			return fail(e)
		}
		if _, e = io.WriteString(w, `}`); e != nil {
			return fail(e)
		}
	}
	if _, e = io.WriteString(w, `]}`); e != nil {
		return fail(e)
	}
	if e = buffer.Flush(); e != nil {
		return fail(e)
	}
	if _, e = f.Seek(0, 0); e != nil {
		return fail(e)
	}
	return f, hex.EncodeToString(hash.Sum(nil)), nil
}
