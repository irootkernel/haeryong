package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
	"regexp"
)

var operationID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// mutation records the exact caller intent before a K/O/natural commit send.
// A missing receipt retains its identity and semantic input on retry.
func mutation(ctx context.Context, api *client.Client, state *clientstate.Store, id, method, target string, body any) (domain.Response, string, error) {
	identity, e := api.Identity(ctx)
	if e != nil {
		return domain.Response{}, id, e
	}
	if id == "" {
		id, e = service.NewID()
		if e != nil {
			return domain.Response{}, id, e
		}
		id, e = state.UseCommandPending(ctx, identity, clientstate.CommandIntent{Method: method, Target: target}, id)
		if e != nil {
			return domain.Response{}, id, e
		}
	}
	if !operationID.MatchString(id) {
		return domain.Response{}, id, invalid("canonical operation ID required")
	}
	release, e := state.Lock(ctx, id)
	if e != nil {
		return domain.Response{}, id, e
	}
	defer release()
	input, e := json.Marshal(body)
	if e != nil {
		return domain.Response{}, id, e
	}
	journal, e := state.Load(id)
	if e == nil {
		if journal.Identity.LibraryID != identity.LibraryID || journal.Identity.PrincipalID != identity.PrincipalID || journal.Method != method || journal.Target != target || !bytes.Equal(journal.FinalBody, input) {
			return domain.Response{}, id, transfer.ErrIntent
		}
		result, e := transfer.AwaitOperation(ctx, api, id)
		if e == nil {
			journal.Response = &result
			journal.Phase = "accepted"
			return result, id, state.Save(journal)
		}
		if !errors.Is(e, domain.ErrNotFound) {
			if result.Status >= 400 {
				journal.Response = &result
				journal.Phase = "failed"
				if saveError := state.Save(journal); saveError != nil {
					return result, id, saveError
				}
			}
			return result, id, e
		}
		if identity.RuntimeGeneration != journal.Identity.RuntimeGeneration || journal.Response != nil {
			return domain.Response{}, id, transfer.ErrReconcile
		}
	} else if errors.Is(e, domain.ErrNotFound) {
		journal = transfer.Journal{OperationID: id, Purpose: "mutation", Identity: identity, Method: method, Target: target, FinalBody: input, Phase: "dispatch"}
		if e = state.Save(journal); e != nil {
			return domain.Response{}, id, e
		}
	} else {
		return domain.Response{}, id, e
	}
	result, e := api.JSON(ctx, method, target, id, json.RawMessage(journal.FinalBody), nil)
	if e != nil {
		if result.Status >= 400 {
			journal.Response = &result
			journal.Phase = "failed"
			if saveError := state.Save(journal); saveError != nil {
				return result, id, saveError
			}
		}
		return result, id, e
	}
	journal.Response = &result
	journal.Phase = "accepted"
	e = state.Save(journal)
	return result, id, e
}

func finishMutation(ctx context.Context, state *clientstate.Store, intent clientstate.CommandIntent, id string) error {
	journal, e := state.Load(id)
	if e != nil {
		return e
	}
	return state.FinishCommandPending(ctx, journal.Identity, intent, id)
}

// A definitive failure can release its automatic pending index only after the
// original status/operation identity has been emitted successfully to the caller.
type recordedCommandError struct {
	cause       error
	operation   string
	status      int
	acknowledge func() error
}

func (e *recordedCommandError) Error() string { return e.cause.Error() }
func (e *recordedCommandError) Unwrap() error { return e.cause }
func recordedError(cause error, response domain.Response, id string, ack func() error) error {
	if response.Status < 400 {
		return cause
	}
	return &recordedCommandError{cause: cause, operation: id, status: response.Status, acknowledge: ack}
}
