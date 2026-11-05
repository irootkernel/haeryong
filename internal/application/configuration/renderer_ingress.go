package configuration

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// RendererMapping describes configuration intent, not an allocated origin or
// reachable listener. Callers supply a configuration accepted by Validate.
type RendererMapping struct {
	VersionID string `json:"version_id"`
	Origin    string `json:"origin"`
	Upstream  string `json:"upstream"`
}

func RendererMappings(value map[string]any) []RendererMapping {
	network, _ := value["network"].(map[string]any)
	configured, _ := network["renderer_mappings"].([]any)
	mappings := make([]RendererMapping, 0, len(configured))
	for _, item := range configured {
		entry := item.(map[string]any)
		mappings = append(mappings, RendererMapping{
			VersionID: entry["version_id"].(string),
			Origin:    entry["origin"].(string),
			Upstream:  entry["upstream"].(string),
		})
	}
	return mappings
}

// originAuthority compares the wire Host, including implicit default ports.
// Scheme does not distinguish two requests with the same Host header.
func originAuthority(origin *url.URL) string {
	port := origin.Port()
	if port == "" {
		if origin.Scheme == "https" {
			port = "443"
		} else if origin.Scheme == "http" {
			port = "80"
		}
	}
	if number, e := strconv.Atoi(port); e == nil {
		port = strconv.Itoa(number)
	}
	return net.JoinHostPort(strings.ToLower(origin.Hostname()), port)
}

// CanonicalOrigin normalizes a valid HTTP or HTTPS origin to its browser URL
// origin (lowercased host, default port omitted, numeric port without leading
// zeros), its corresponding browser wire Host, and its scheme-independent
// host:port authority.
func CanonicalOrigin(raw string) (string, string, string, bool) {
	if raw == "" || strings.ContainsAny(raw, "*\\\r\n\t ?#") {
		return "", "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Host == "" || strings.ContainsAny(u.Host, "/") || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || raw != u.Scheme+"://"+u.Host {
		return "", "", "", false
	}
	host := strings.ToLower(u.Hostname())
	defaultPort := 80
	if u.Scheme == "https" {
		defaultPort = 443
	}
	effectivePort := defaultPort
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", "", "", false
		}
		effectivePort = n
	}
	authority := net.JoinHostPort(host, strconv.Itoa(effectivePort))
	wireHost := authority
	if effectivePort == defaultPort {
		if strings.Contains(host, ":") {
			wireHost = "[" + host + "]"
		} else {
			wireHost = host
		}
	}
	return u.Scheme + "://" + wireHost, wireHost, authority, true
}

// PrivateRendererMappingReason describes the supported same-host HTTPS
// construction without opening storage or a socket. Empty means configured
// support only; actual ownership, bind and reachability checks remain at Open.
func PrivateRendererMappingReason(parent string, mapping RendererMapping, local string) string {
	const unavailable = "renderer_remote_ingress"
	management, e := url.Parse(parent)
	if e != nil || management.Scheme != "https" || management.Host == "" {
		return unavailable
	}
	renderer, e := url.Parse(mapping.Origin)
	if e != nil || renderer.Scheme != "https" || !strings.EqualFold(management.Hostname(), renderer.Hostname()) {
		return unavailable
	}
	upstream, e := url.Parse(mapping.Upstream)
	if e != nil || upstream.Scheme != "http" || upstream.Hostname() != "127.0.0.1" || upstream.Port() == "" {
		return unavailable
	}
	localManagement, e := url.Parse(local)
	if e != nil || localManagement.Host == "" {
		return unavailable
	}
	parentHost, rendererHost := originAuthority(management), originAuthority(renderer)
	localHost, upstreamHost := originAuthority(localManagement), originAuthority(upstream)
	if rendererHost == parentHost || rendererHost == localHost || rendererHost == upstreamHost || upstreamHost == parentHost || upstreamHost == localHost {
		return unavailable
	}
	return ""
}
