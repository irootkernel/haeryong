package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"haeryong.local/haeryong/internal/adapters/client"
	config "haeryong.local/haeryong/internal/application/configuration"
)

type doctorMapping struct {
	config.RendererMapping
	Configured bool `json:"configured"`
	Supported  bool `json:"supported"`
	Reason     any  `json:"reason"`
}

type doctorEndpoints struct {
	ConnectedEndpoint       string            `json:"connected_endpoint"`
	ConfiguredLocalEndpoint string            `json:"configured_local_endpoint"`
	RuntimeLocalEndpoint    string            `json:"runtime_local_endpoint"`
	EphemeralOverride       bool              `json:"ephemeral_override"`
	RemoteEnabled           bool              `json:"remote_enabled"`
	ExternalBaseURL         any               `json:"external_base_url"`
	RendererMappings        []doctorMapping   `json:"renderer_mappings"`
	Reachability            map[string]string `json:"reachability"`
	LocalRecovery           map[string]string `json:"local_recovery"`
}

func endpointChecks(connected string, status map[string]json.RawMessage, view config.View) (doctorEndpoints, error) {
	var revision int64
	if e := json.Unmarshal(status["config_revision"], &revision); e != nil {
		return doctorEndpoints{}, fmt.Errorf("invalid status configuration revision: %w", e)
	}
	if revision != view.ConfigRevision {
		return doctorEndpoints{}, commandError{4, fmt.Errorf("configuration changed during endpoint checks; retry doctor")}
	}
	var listener struct {
		Endpoint          string `json:"endpoint"`
		EphemeralOverride bool   `json:"ephemeral_override"`
	}
	if e := json.Unmarshal(status["listener"], &listener); e != nil {
		return doctorEndpoints{}, fmt.Errorf("invalid status listener: %w", e)
	}
	value, e := config.Decode(bytes.NewReader(view.Value), "json")
	if e != nil {
		return doctorEndpoints{}, e
	}
	network := value["network"].(map[string]any)
	configured := network["listener"].(map[string]any)
	local := "http://" + configured["bind"].(string) + ":" + fmt.Sprint(configured["port"])
	parent, _ := network["external_base_url"].(string)
	remoteEnabled := network["remote_enabled"] == true
	mappings := []doctorMapping{}
	for _, mapping := range config.RendererMappings(value) {
		reason := "renderer_remote_ingress"
		if remoteEnabled {
			reason = config.PrivateRendererMappingReason(parent, mapping, listener.Endpoint)
		}
		var unavailable any
		if reason != "" {
			unavailable = reason
		}
		mappings = append(mappings, doctorMapping{mapping, true, reason == "", unavailable})
	}
	return doctorEndpoints{
		ConnectedEndpoint: connected, ConfiguredLocalEndpoint: local,
		RuntimeLocalEndpoint: listener.Endpoint, EphemeralOverride: listener.EphemeralOverride,
		RemoteEnabled: remoteEnabled, ExternalBaseURL: network["external_base_url"], RendererMappings: mappings,
		Reachability: map[string]string{"connected_endpoint": "verified", "other_endpoints": "not_tested"},
		LocalRecovery: map[string]string{
			"configured_endpoint": local, "runtime_endpoint": listener.Endpoint,
			"instruction": "Use haeryong daemon run with the same --profile/--data-dir selection. For a port conflict choose an explicit unused --port, then rerun doctor through the verified local endpoint; --port is ephemeral.",
		},
	}, nil
}

// selectedClient has already verified the captured endpoint/library credential.
// These two authenticated reads do not probe any configured secondary endpoint.
func doctorCommand(ctx context.Context, api *client.Client, emit func(any) error) error {
	var status map[string]json.RawMessage
	if _, e := api.JSON(ctx, "GET", "/api/v1/status", "", nil, &status); e != nil {
		return productError(e)
	}
	var view config.View
	if _, e := api.JSON(ctx, "GET", "/api/v1/configuration", "", nil, &view); e != nil {
		return productError(e)
	}
	checks, e := endpointChecks(api.Endpoint(), status, view)
	if e != nil {
		return e
	}
	encoded, e := json.Marshal(checks)
	if e != nil {
		return e
	}
	// Retain the original status values verbatim, including large JSON integers.
	status["endpoint_checks"] = encoded
	return emit(status)
}
