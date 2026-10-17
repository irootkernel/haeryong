package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
	"net/http"
	"path/filepath"
)

// connectCommand uses only the frozen create/poll pairing protocol. Approval is
// always a separate explicit owner operation; local control is never a fallback
// that silently approves this client or reveals a management bearer.
func connectCommand(ctx context.Context, opt options, emit func(any) error) error {
	if len(opt.command) > 3 || len(opt.command) == 3 && opt.command[2] != "new" {
		return invalid("use auth connect [new]")
	}
	root, e := clientDirectory(opt)
	if e != nil {
		return e
	}
	state, e := clientstate.New(root)
	if e != nil {
		return e
	}
	path := opt.flags["credential"]
	if path == "" {
		path = filepath.Join(root, "credential.json")
	}
	endpoint := opt.endpoint
	if endpoint == "" {
		if saved, e := client.LoadCredential(path); e == nil {
			endpoint = saved.Endpoint
		} else {
			profile, e := service.Load(opt.selection)
			if e != nil {
				return e
			}
			discovery, e := service.ReadDiscovery(profile)
			if e != nil {
				return e
			}
			identity, e := localcontrol.Verify(ctx, profile, discovery)
			if e != nil {
				return e
			}
			endpoint = identity.Endpoint
		}
	}
	endpoint, e = client.Origin(endpoint)
	if e != nil {
		return invalid(e.Error())
	}
	if len(opt.command) != 3 {
		if saved, e := client.LoadCredential(path); e == nil {
			api, e := client.New(endpoint, opt.selection.Profile, saved)
			if e != nil {
				return commandError{3, e}
			}
			identity, e := api.Identity(ctx)
			if e == nil {
				return emit(map[string]any{"connected": true, "library_id": identity.LibraryID, "principal_id": identity.PrincipalID, "endpoint": endpoint})
			}
			var failure *client.Error
			if !errors.As(e, &failure) || failure.Status != 401 {
				return productError(e)
			}
		}
	} else {
		if e = state.ClearPairing(); e != nil {
			return e
		}
	}
	public := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("pairing redirect rejected") }}
	call := func(path string, body any, out any) error {
		encoded, e := json.Marshal(body)
		if e != nil {
			return e
		}
		r, e := http.NewRequestWithContext(ctx, "POST", endpoint+path, bytes.NewReader(encoded))
		if e != nil {
			return e
		}
		r.Header.Set("Origin", endpoint)
		r.Header.Set("Content-Type", "application/json")
		response, e := public.Do(r)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		var value struct {
			Data  json.RawMessage `json:"data"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if e = json.NewDecoder(response.Body).Decode(&value); e != nil {
			return e
		}
		if response.StatusCode >= 400 {
			return &client.Error{Status: response.StatusCode, Code: value.Error.Code}
		}
		return json.Unmarshal(value.Data, out)
	}
	pending, e := state.Pairing()
	if e != nil && !errors.Is(e, domain.ErrNotFound) {
		return e
	}
	if errors.Is(e, domain.ErrNotFound) {
		pending = clientstate.Pairing{Endpoint: endpoint, Profile: opt.selection.Profile}
		if e = call("/api/v1/auth/pairings", map[string]string{"device_label": "Haeryong CLI (" + opt.selection.Profile + ")"}, &pending.Value); e != nil {
			return productError(e)
		}
		if e = state.SavePairing(pending); e != nil {
			return e
		}
		return emit(map[string]any{"connected": false, "state": "pending", "pairing_id": pending.Value.ID, "code": pending.Value.Code, "etag": pending.Value.ETag, "origin": endpoint, "expires_at": pending.Value.ExpiresAt})
	}
	if pending.Endpoint != endpoint || pending.Profile != opt.selection.Profile {
		return commandError{3, errors.New("pending pairing endpoint/profile mismatch")}
	}
	var polled domain.PairingPoll
	if e = call("/api/v1/auth/pairings/"+pending.Value.ID+"/poll", map[string]string{"polling_secret": pending.Value.PollingSecret}, &polled); e != nil {
		return productError(e)
	}
	if polled.Token == "" {
		return emit(map[string]any{"connected": false, "state": polled.State, "pairing_id": pending.Value.ID, "code": pending.Value.Code, "etag": pending.Value.ETag, "origin": endpoint, "expires_at": pending.Value.ExpiresAt})
	}
	if polled.Session == nil {
		return commandError{6, errors.New("redemption outcome missing; start a new pairing")}
	}
	credential := client.Credential{Endpoint: endpoint, Profile: opt.selection.Profile, LibraryID: polled.Session.LibraryID, Token: polled.Token}
	if e = client.SaveCredential(path, credential); e != nil {
		return commandError{6, fmt.Errorf("credential could not be durably remembered; start a new pairing: %w", e)}
	}
	if e = state.ClearPairing(); e != nil {
		return e
	}
	return emit(map[string]any{"connected": true, "library_id": credential.LibraryID, "principal_id": polled.Session.PrincipalID, "session_id": polled.Session.ID, "endpoint": endpoint})
}
