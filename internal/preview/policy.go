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
)
