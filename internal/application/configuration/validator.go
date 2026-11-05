// Package configuration owns the single effective-policy validator and exact
// plan semantics. The embedded schema is the selected config closure of the
// repository's machine contract; tests compare it with that authority.
package configuration

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	yaml "go.yaml.in/yaml/v3"
	"haeryong.local/haeryong/internal/application/origins"
	"io"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed schema.json
var schemaBytes []byte
var definitions = func() map[string]any {
	var s map[string]any
	if e := json.Unmarshal(schemaBytes, &s); e != nil {
		panic(e)
	}
	return s["$defs"].(map[string]any)
}()

type Error struct{ Field, Reason string }

func (e *Error) Error() string          { return "INVALID_REQUEST: " + e.Field + ": " + e.Reason }
func invalid(path, reason string) error { return &Error{path, reason} }
func pointer(s string) string           { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }

// Decode accepts exactly one strict JSON or safe YAML configuration value.
func Decode(r io.Reader, format string) (map[string]any, error) {
	var value any
	var err error
	switch format {
	case "json", "":
		raw, e := io.ReadAll(r)
		if e != nil {
			return nil, e
		}
		if !utf8.Valid(raw) || !validEscapes(raw) {
			return nil, invalid("", "invalid Unicode")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		value, err = jsonValue(d, "")
		if err == nil {
			if _, e := d.Token(); e != io.EOF {
				err = invalid("", "one JSON document required")
			}
		}
	case "yaml":
		var n yaml.Node
		d := yaml.NewDecoder(r)
		if err = d.Decode(&n); err == nil {
			if len(n.Content) != 1 {
				err = invalid("", "one YAML value required")
			} else {
				value, err = yamlValue(n.Content[0], "")
			}
		}
		if err == nil {
			var extra yaml.Node
			if e := d.Decode(&extra); e != io.EOF {
				err = invalid("", "one YAML document required")
			}
		}
	default:
		return nil, invalid("", "unsupported configuration format")
	}
	if err != nil {
		return nil, err
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, invalid("", "object required")
	}
	return m, nil
}
func jsonValue(d *json.Decoder, path string) (any, error) {
	t, e := d.Token()
	if e != nil {
		return nil, invalid(path, "invalid JSON")
	}
	if delimiter, ok := t.(json.Delim); ok {
		switch delimiter {
		case '{':
			m := map[string]any{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return nil, invalid(path, "invalid object")
				}
				k, ok := key.(string)
				if !ok {
					return nil, invalid(path, "invalid key")
				}
				if _, exists := m[k]; exists {
					return nil, invalid(path+"/"+pointer(k), "duplicate field")
				}
				v, e := jsonValue(d, path+"/"+pointer(k))
				if e != nil {
					return nil, e
				}
				m[k] = v
			}
			if _, e := d.Token(); e != nil {
				return nil, e
			}
			return m, nil
		case '[':
			out := []any{}
			for d.More() {
				v, e := jsonValue(d, path+"/"+strconv.Itoa(len(out)))
				if e != nil {
					return nil, e
				}
				out = append(out, v)
			}
			if _, e := d.Token(); e != nil {
				return nil, e
			}
			return out, nil
		}
		return nil, invalid(path, "invalid delimiter")
	}
	if s, ok := t.(string); ok && !utf8.ValidString(s) {
		return nil, invalid(path, "invalid UTF-8")
	}
	return t, nil
}
func yamlValue(n *yaml.Node, path string) (any, error) {
	if n.Anchor != "" || n.Alias != nil || n.Kind == yaml.AliasNode || n.Style&yaml.TaggedStyle != 0 {
		return nil, invalid(path, "YAML tags and expansion are unsupported")
	}
	switch n.Kind {
	case yaml.MappingNode:
		if n.Tag != "!!map" {
			return nil, invalid(path, "unsafe YAML node")
		}
		m := map[string]any{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || k.Value == "<<" || k.Anchor != "" || k.Style&yaml.TaggedStyle != 0 {
				return nil, invalid(path, "string field required")
			}
			if _, ok := m[k.Value]; ok {
				return nil, invalid(path+"/"+pointer(k.Value), "duplicate field")
			}
			v, e := yamlValue(n.Content[i+1], path+"/"+pointer(k.Value))
			if e != nil {
				return nil, e
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		if n.Tag != "!!seq" {
			return nil, invalid(path, "unsafe YAML node")
		}
		out := []any{}
		for i, c := range n.Content {
			v, e := yamlValue(c, path+"/"+strconv.Itoa(i))
			if e != nil {
				return nil, e
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str":
			if !utf8.ValidString(n.Value) {
				return nil, invalid(path, "invalid UTF-8")
			}
			return n.Value, nil
		case "!!null":
			return nil, nil
		case "!!bool":
			var v bool
			if e := n.Decode(&v); e != nil {
				return nil, invalid(path, "invalid boolean")
			}
			return v, nil
		case "!!int":
			var v int64
			if e := n.Decode(&v); e != nil {
				return nil, invalid(path, "invalid integer")
			}
			return json.Number(strconv.FormatInt(v, 10)), nil
		case "!!float":
			v := json.Number(n.Value)
			if _, e := strconv.ParseFloat(string(v), 64); e != nil || strings.ContainsAny(string(v), "NnIi") {
				return nil, invalid(path, "finite number required")
			}
			return v, nil
		}
		return nil, invalid(path, "unsafe YAML scalar")
	}
	return nil, invalid(path, "unsupported YAML node")
}
func Validate(value map[string]any) error {
	if e := shape(definitions["config"].(map[string]any), value, ""); e != nil {
		return e
	}
	network := value["network"].(map[string]any)
	listener := network["listener"].(map[string]any)
	local := "http://127.0.0.1:" + fmt.Sprint(listener["port"])
	localEndpoint, _ := url.Parse(local)
	localHost := originAuthority(localEndpoint)
	trusted := map[string]bool{local: true}
	trustedHosts := map[string]bool{localHost: true}
	if network["remote_enabled"] == true {
		origin, ok := network["external_base_url"].(string)
		if !ok {
			return invalid("/network/external_base_url", "remote HTTPS origin required")
		}
		trusted[origin] = true
		// Static collision guard for the no-override path: the request Host
		// header carries no scheme, so an external URL differing only by
		// scheme from the configured listener host is indistinguishable on
		// the wire. The runtime effective-endpoint check covers a CLI
		// listener override against its actual endpoint.
		if remote, err := url.Parse(origin); err == nil {
			remoteHost := originAuthority(remote)
			if remoteHost == localHost {
				return invalid("/network/external_base_url", "external host must differ from the local listener host")
			}
			trustedHosts[remoteHost] = true
		}
	}
	allowed := map[string]bool{}
	for _, v := range network["allowed_origins"].([]any) {
		allowed[v.(string)] = true
	}
	if !reflect.DeepEqual(allowed, trusted) {
		return invalid("/network/allowed_origins", "exact configured management endpoints required")
	}
	seen := map[string]bool{}
	seenHosts := map[string]bool{}
	seenVersions := map[string]bool{}
	if mappings, ok := network["renderer_mappings"].([]any); ok {
		for _, v := range mappings {
			entry := v.(map[string]any)
			versionID := entry["version_id"].(string)
			if seenVersions[versionID] {
				return invalid("/network/renderer_mappings", "each version may have at most one active renderer mapping")
			}
			seenVersions[versionID] = true
			o := entry["origin"].(string)
			u := entry["upstream"].(string)
			originURL, originErr := url.Parse(o)
			upstreamURL, upstreamErr := url.Parse(u)
			if originErr != nil || upstreamErr != nil {
				return invalid("/network/renderer_mappings", "valid origin and upstream required")
			}
			upstreamPort, portErr := strconv.Atoi(upstreamURL.Port())
			if portErr != nil || upstreamPort < 1024 || upstreamPort > 65535 {
				return invalid("/network/renderer_mappings", "renderer upstream port must be between 1024 and 65535")
			}
			if origins.LegacyReserved(upstreamPort) {
				return invalid("/network/renderer_mappings", "renderer upstream must not use the frozen legacy fixture origins 9272-9273")
			}
			originHost := originAuthority(originURL)
			upstreamHost := originAuthority(upstreamURL)
			if seen[o] || trusted[o] || originHost == upstreamHost ||
				trustedHosts[originHost] || trustedHosts[upstreamHost] ||
				seenHosts[originHost] || seenHosts[upstreamHost] {
				return invalid("/network/renderer_mappings", "unique separate origins required")
			}
			seen[o] = true
			seenHosts[originHost] = true
			seenHosts[upstreamHost] = true
		}
	}
	if pool, ok := network["renderer_pool"].(map[string]any); ok {
		base, e := integerValue(pool["base_port"], "/network/renderer_pool/base_port")
		if e != nil {
			return e
		}
		count, e := integerValue(pool["count"], "/network/renderer_pool/count")
		if e != nil {
			return e
		}
		last := base + count - 1
		if last > 65535 {
			return invalid("/network/renderer_pool/count", "range exceeds the port space")
		}
		if base <= origins.LegacyPortLast && last >= origins.LegacyPortFirst {
			return invalid("/network/renderer_pool/base_port", "range intersects the frozen legacy fixture origins 9272-9273")
		}
		if management, e := integerValue(listener["port"], "/network/listener/port"); e == nil && management >= base && management <= last {
			return invalid("/network/renderer_pool", "range must not include the management listener port")
		}
		if mappings, ok := network["renderer_mappings"].([]any); ok {
			for _, v := range mappings {
				upstream := v.(map[string]any)["upstream"].(string)
				parsed, e := url.Parse(upstream)
				if e != nil {
					continue
				}
				port, e := strconv.Atoi(parsed.Port())
				if e == nil && port >= base && port <= last {
					return invalid("/network/renderer_pool", "range must not include a renderer mapping upstream port")
				}
			}
		}
	}
	maintenance := value["maintenance"].(map[string]any)
	if e := schedule(maintenance["schedule"].(map[string]any), "/maintenance/schedule"); e != nil {
		return e
	}
	for id, item := range value["projects"].(map[string]any) {
		path := "/projects/" + pointer(id)
		if e := shape(definitions["id"].(map[string]any), id, path); e != nil {
			return e
		}
		settings := item.(map[string]any)
		if override, ok := settings["maintenance"].(map[string]any); ok {
			merged := Merge(maintenance, override)
			if e := shape(definitions["maintenance"].(map[string]any), merged, path+"/maintenance"); e != nil {
				return e
			}
			if e := schedule(merged["schedule"].(map[string]any), path+"/maintenance/schedule"); e != nil {
				return e
			}
		}
		if sources, ok := settings["sources"].([]any); ok {
			ids := map[string]bool{}
			for i, v := range sources {
				source := v.(map[string]any)
				sid := source["source_id"].(string)
				if ids[sid] {
					return invalid(path+"/sources/"+strconv.Itoa(i), "duplicate source identity")
				}
				ids[sid] = true
				if source["kind"] == "git" && source["repo_id"] == nil {
					return invalid(path+"/sources/"+strconv.Itoa(i)+"/repo_id", "Git repository identity required")
				}
			}
		}
	}
	return nil
}

// Default renderer pool. The base starts at 9281: the G-VIEW spike fixture
// origins (9272/9273, trusted 9270) may still hold authored Service Workers
// in preserved browser profiles on this machine, and production defaults
// must not collide with them. The range is an address-space mechanism, never
// a content or version admission limit.
const (
	DefaultRendererPoolBase  = 9281
	DefaultRendererPoolCount = 8
)

// RendererPool returns the effective renderer origin pool of a decoded
// configuration value. Profiles stored before the key existed resolve to the
// code default; new libraries receive the same value from defaults.json.
func RendererPool(value map[string]any) (int, int) {
	network, _ := value["network"].(map[string]any)
	pool, _ := network["renderer_pool"].(map[string]any)
	base, baseErr := integerValue(pool["base_port"], "")
	count, countErr := integerValue(pool["count"], "")
	if baseErr != nil || countErr != nil || count < 1 || base < 1024 || base+count-1 > 65535 {
		return DefaultRendererPoolBase, DefaultRendererPoolCount
	}
	return base, count
}

// integerValue accepts the strict integer forms produced by Decode and
// plain JSON decoding of defaults.
func integerValue(v any, path string) (int, error) {
	switch number := v.(type) {
	case json.Number:
		value, e := strconv.Atoi(number.String())
		if e != nil {
			return 0, invalid(path, "integer required")
		}
		return value, nil
	case float64:
		if number != float64(int(number)) {
			return 0, invalid(path, "integer required")
		}
		return int(number), nil
	}
	return 0, invalid(path, "integer required")
}

// networkPool summarizes the effective renderer pool of a decoded
// configuration value for plan-impact comparison.
func networkPool(value map[string]any) struct{ base, count int } {
	base, count := RendererPool(value)
	return struct{ base, count int }{base, count}
}

func shape(s map[string]any, v any, path string) error {
	if ref, ok := s["$ref"].(string); ok {
		return shape(definitions[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any), v, path)
	}
	if alternatives, ok := s["anyOf"].([]any); ok {
		for _, a := range alternatives {
			if shape(a.(map[string]any), v, path) == nil {
				return nil
			}
		}
		return invalid(path, "value does not match allowed types")
	}
	if values, ok := s["enum"].([]any); ok {
		for _, x := range values {
			if reflect.DeepEqual(x, v) {
				return nil
			}
		}
		return invalid(path, "unsupported value")
	}
	kind, _ := s["type"].(string)
	switch kind {
	case "null":
		if v != nil {
			return invalid(path, "null required")
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return invalid(path, "boolean required")
		}
	case "string":
		text, ok := v.(string)
		if !ok {
			return invalid(path, "string required")
		}
		if minimum, ok := s["minLength"].(float64); ok && utf8.RuneCountInString(text) < int(minimum) {
			return invalid(path, "string is empty")
		}
		if p, ok := s["pattern"].(string); ok {
			if matched, e := regexp.MatchString(p, text); e != nil || !matched {
				return invalid(path, "invalid string")
			}
		}
		if f, ok := s["format"].(string); ok {
			if e := format(f, text); e != nil {
				return invalid(path, e.Error())
			}
		}
	case "integer":
		number, ok := v.(json.Number)
		if !ok {
			return invalid(path, "integer required")
		}
		integer, e := number.Int64()
		if e != nil {
			return invalid(path, "integer required")
		}
		for key, sign := range map[string]int{"minimum": -1, "maximum": 1} {
			if bound, ok := s[key].(float64); ok {
				if sign < 0 && integer < int64(bound) || sign > 0 && integer > int64(bound) {
					return invalid(path, "integer outside allowed range")
				}
			}
		}
	case "array":
		list, ok := v.([]any)
		if !ok {
			return invalid(path, "array required")
		}
		for i, item := range list {
			if e := shape(s["items"].(map[string]any), item, path+"/"+strconv.Itoa(i)); e != nil {
				return e
			}
		}
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return invalid(path, "object required")
		}
		properties, _ := s["properties"].(map[string]any)
		if required, ok := s["required"].([]any); ok {
			for _, k := range required {
				if _, ok := m[k.(string)]; !ok {
					return invalid(path+"/"+pointer(k.(string)), "required field missing")
				}
			}
		}
		for k, item := range m {
			child, ok := properties[k].(map[string]any)
			if !ok {
				switch a := s["additionalProperties"].(type) {
				case bool:
					if !a {
						return invalid(path+"/"+pointer(k), "unknown field")
					}
					continue
				case map[string]any:
					child = a
				default:
					continue
				}
			}
			if e := shape(child, item, path+"/"+pointer(k)); e != nil {
				return e
			}
		}
	}
	return nil
}

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func format(kind, text string) error {
	if kind == "uuid" {
		if !uuid.MatchString(text) {
			return fmt.Errorf("canonical UUID required")
		}
		return nil
	}
	u, e := url.Parse(text)
	if e != nil || u.User != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || text != u.Scheme+"://"+u.Host || strings.ContainsAny(text, "* \r\n\t") {
		return fmt.Errorf("exact origin required")
	}
	switch kind {
	case "https-origin":
		if u.Scheme != "https" {
			return fmt.Errorf("HTTPS origin required")
		}
	case "loopback-origin":
		if u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
			return fmt.Errorf("loopback origin required")
		}
	case "origin":
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("HTTP origin required")
		}
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid origin port")
		}
	}
	return nil
}

var term = regexp.MustCompile(`^(\*|[0-9]+(?:-[0-9]+)?)(?:/([0-9]+))?$`)

func schedule(m map[string]any, path string) error {
	zone := m["timezone"].(string)
	if _, e := time.LoadLocation(zone); e != nil || zone == "Local" {
		return invalid(path+"/timezone", "unknown IANA timezone")
	}
	mode := m["mode"]
	interval, cron := m["interval_seconds"], m["cron"]
	if mode == "manual" && (interval != nil || cron != nil) || mode == "interval" && (interval == nil || cron != nil) || mode == "calendar" && (interval != nil || cron == nil) {
		return invalid(path, "mode-specific fields conflict")
	}
	if cron == nil {
		return nil
	}
	fields := strings.Fields(cron.(string))
	if len(fields) != 5 {
		return invalid(path+"/cron", "five fields required")
	}
	for i, field := range fields {
		low, high := []int{0, 0, 1, 1, 0}[i], []int{59, 23, 31, 12, 6}[i]
		for _, p := range strings.Split(field, ",") {
			match := term.FindStringSubmatch(p)
			if match == nil {
				return invalid(path+"/cron", "invalid cron term")
			}
			if match[2] != "" {
				step, e := strconv.Atoi(match[2])
				if e != nil || step < 1 {
					return invalid(path+"/cron", "invalid cron step")
				}
			}
			if match[1] != "*" {
				parts := strings.Split(match[1], "-")
				a, e := strconv.Atoi(parts[0])
				if e != nil || a < low || a > high {
					return invalid(path+"/cron", "cron range outside field")
				}
				if len(parts) == 2 {
					b, e := strconv.Atoi(parts[1])
					if e != nil || b < a || b > high {
						return invalid(path+"/cron", "cron range outside field")
					}
				}
			}
		}
	}
	return nil
}

// Merge copies nested objects, replaces arrays, and preserves explicit null.
func Merge(current, changes map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range current {
		out[k] = v
	}
	for k, v := range changes {
		if m, ok := v.(map[string]any); ok {
			old, _ := out[k].(map[string]any)
			out[k] = Merge(old, m)
		} else {
			out[k] = v
		}
	}
	return out
}
func Canonical(value map[string]any) []byte { b, _ := json.Marshal(value); return bytes.Clone(b) }

// EncodeYAML preserves JSON numeric types instead of treating json.Number as text.
func EncodeYAML(value map[string]any) ([]byte, error) {
	var node func(any) *yaml.Node
	node = func(v any) *yaml.Node {
		switch x := v.(type) {
		case map[string]any:
			n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, node(x[k]))
			}
			return n
		case []any:
			n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, i := range x {
				n.Content = append(n.Content, node(i))
			}
			return n
		case json.Number:
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: string(x)}
		default:
			n := &yaml.Node{}
			_ = n.Encode(v)
			return n
		}
	}
	return yaml.Marshal(node(value))
}

// JSON's decoder replaces unpaired surrogate escapes. Policy paths and IDs must
// retain their exact spelling instead of silently selecting a different path.
func validEscapes(raw []byte) bool {
	inside := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inside = !inside
			continue
		}
		if !inside || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		code, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
