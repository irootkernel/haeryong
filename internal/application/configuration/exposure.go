package configuration

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

type exposureSnapshot struct {
	RemoteEnabled           bool              `json:"remote_enabled"`
	ManagementOrigin        any               `json:"management_origin"`
	ConfiguredLocalEndpoint string            `json:"configured_local_endpoint"`
	LocalForwardingEndpoint string            `json:"local_forwarding_endpoint"`
	RendererMappings        []RendererMapping `json:"renderer_mappings"`
}

type exposureImpact struct {
	Changed                           bool             `json:"changed"`
	Before                            exposureSnapshot `json:"before"`
	After                             exposureSnapshot `json:"after"`
	ProxyConfigurationChangedByCommit bool             `json:"proxy_configuration_changed_by_commit"`
	PrivateProxyAction                string           `json:"private_proxy_action"`
}

func (s *Service) exposureSnapshot(value map[string]any) exposureSnapshot {
	network := value["network"].(map[string]any)
	listener := network["listener"].(map[string]any)
	endpoint := "http://" + listener["bind"].(string) + ":" + fmt.Sprint(listener["port"])
	forwarding := endpoint
	if s.ListenerOverride && s.RuntimeEndpoint != "" {
		forwarding = s.RuntimeEndpoint
	}
	mappings := RendererMappings(value)
	sort.Slice(mappings, func(i, j int) bool {
		left, right := mappings[i], mappings[j]
		if left.VersionID != right.VersionID {
			return left.VersionID < right.VersionID
		}
		if left.Origin != right.Origin {
			return left.Origin < right.Origin
		}
		return left.Upstream < right.Upstream
	})
	return exposureSnapshot{
		RemoteEnabled: network["remote_enabled"] == true, ManagementOrigin: network["external_base_url"],
		ConfiguredLocalEndpoint: endpoint, LocalForwardingEndpoint: forwarding, RendererMappings: mappings,
	}
}

func (s *Service) exposureImpact(old, next map[string]any) exposureImpact {
	before, after := s.exposureSnapshot(old), s.exposureSnapshot(next)
	changed := !reflect.DeepEqual(before, after)
	action := "none"
	if changed {
		action = "inspect_apply_and_verify_private_proxy_separately"
	}
	return exposureImpact{Changed: changed, Before: before, After: after, PrivateProxyAction: action}
}

func hasExposurePreview(impact json.RawMessage) bool {
	var value struct {
		Exposure *exposureImpact `json:"exposure"`
	}
	return json.Unmarshal(impact, &value) == nil && value.Exposure != nil
}
