package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

func productHandler(core *appauth.Service, local string, configuration []byte) (http.Handler, error) {
	var c struct {
		Network struct {
			RemoteEnabled bool     `json:"remote_enabled"`
			External      *string  `json:"external_base_url"`
			Allowed       []string `json:"allowed_origins"`
		} `json:"network"`
	}
	if err := json.Unmarshal(configuration, &c); err != nil {
		return nil, err
	}
	u, err := url.Parse(local)
	if err != nil {
		return nil, err
	}
	policy := httpauth.Policy{Hosts: []string{u.Host}, Origins: []string{local}}
	// Explicit daemon --port selects its actual local origin. Remote trust remains
	// an exact configured HTTPS endpoint, never inferred from forwarded headers.
	if c.Network.RemoteEnabled {
		if c.Network.External == nil {
			return nil, errors.New("remote endpoint configuration missing")
		}
		remote, err := url.Parse(*c.Network.External)
		if err != nil || remote.Scheme != "https" || remote.Host == "" || remote.User != nil || remote.Path != "" || remote.RawQuery != "" || remote.Fragment != "" {
			return nil, errors.New("invalid configured private HTTPS origin")
		}
		allowed := false
		for _, origin := range c.Network.Allowed {
			if origin == *c.Network.External {
				allowed = true
			}
		}
		if !allowed {
			return nil, errors.New("private HTTPS endpoint is not an allowed origin")
		}
		policy.Hosts = append(policy.Hosts, remote.Host)
		policy.Origins = append(policy.Origins, *c.Network.External)
	}
	return httpauth.New(core, policy, nil)
}
func bootstrapCommand(ctx context.Context, opt options, emit func(any) error, input io.Reader) error {
	if opt.endpoint != "" {
		return invalid("local bootstrap uses verified profile discovery")
	}
	p, err := service.Load(opt.selection)
	if err != nil {
		return err
	}
	id, err := service.ReadDiscovery(p)
	if err != nil {
		return err
	}
	if len(opt.command) == 2 && opt.command[1] == "pairings" {
		rows, err := localcontrol.BootstrapPairings(ctx, p, id)
		if err != nil {
			return err
		}
		return emit(rows)
	}
	if len(opt.command) != 3 || (opt.command[1] != "approve" && opt.command[1] != "reject") {
		return invalid("use auth pairings, or auth approve/reject <pairing_id> with exact JSON intent on stdin")
	}
	body, err := io.ReadAll(io.LimitReader(input, 16385))
	if err != nil {
		return err
	}
	if len(body) > 16384 {
		return invalid("bootstrap control envelope too large")
	}
	var v struct {
		Code        string `json:"code"`
		Origin      string `json:"origin"`
		ETag        string `json:"etag"`
		OperationID string `json:"operation_id"`
	}
	// The stdin intent is a small strict control envelope, not authored content.
	scan := json.NewDecoder(bytes.NewReader(body))
	token, e := scan.Token()
	if e != nil || token != json.Delim('{') {
		return invalid("bootstrap intent must be an object")
	}
	seen := map[string]bool{}
	for scan.More() {
		token, e := scan.Token()
		if e != nil {
			return invalid("invalid bootstrap intent")
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return invalid("duplicate bootstrap field")
		}
		seen[key] = true
		var raw json.RawMessage
		if scan.Decode(&raw) != nil || bytes.Equal(raw, []byte("null")) {
			return invalid("invalid bootstrap field")
		}
	}
	if _, e := scan.Token(); e != nil || len(seen) != 4 {
		return invalid("exact bootstrap fields required")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&v) != nil || v.Code == "" || v.Origin == "" || v.ETag == "" || v.OperationID == "" {
		return invalid("exact code, origin, etag and operation_id are required on stdin")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return invalid("one bootstrap intent required")
	}
	out, err := localcontrol.BootstrapDecision(ctx, p, id, domain.PairingDecision{ID: opt.command[2], Action: opt.command[1], Code: v.Code, Origin: v.Origin, ETag: v.ETag, OperationID: v.OperationID})
	if err != nil {
		var failure *localcontrol.BootstrapError
		if errors.As(err, &failure) {
			if failure.Status == 400 {
				return commandError{2, err}
			}
			if failure.Status == 409 || failure.Status == 412 || failure.Status == 428 {
				return commandError{4, err}
			}
		}
		return err
	}
	return emit(out)
}
