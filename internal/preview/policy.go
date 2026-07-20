// Package preview defines the shared publication-policy vocabulary used by
// the control plane, vmd, and edge proxy.
package preview

const (
	// AccessLegacyPublic preserves the pre-publication behavior where every
	// listening non-privileged port is routable.
	AccessLegacyPublic = "legacy_public"
	// AccessPublic requires explicit publication; published ports route without
	// Superserve authentication.
	AccessPublic = "public"

	// HostCapabilityPorts is advertised by a vmd/proxy build that persists and
	// enforces the explicit published-port allowlist.
	HostCapabilityPorts = "preview_ports_v1"

	// ProxyProtocolHeader marks local instance lookups made by a proxy that
	// understands and enforces HostCapabilityPorts. VMD withholds strict-policy
	// instances from callers that omit the marker so rolling the proxy back
	// cannot silently restore all-port routing.
	ProxyProtocolHeader = "X-Superserve-Proxy-Protocol"
)
