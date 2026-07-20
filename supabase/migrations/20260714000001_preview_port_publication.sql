-- Phase 1: explicit publication for public preview ports.
--
-- Existing sandboxes retain the historical behavior where every listening
-- non-privileged port is routable. The new control plane explicitly creates a
-- policy row for each new sandbox; an absent row therefore remains the
-- rolling-deploy-safe legacy representation used by older binaries.

CREATE TABLE sandbox_preview_policy (
    sandbox_id uuid PRIMARY KEY REFERENCES sandbox (id) ON DELETE CASCADE,
    access text NOT NULL,
    revision bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sandbox_preview_policy_access_valid
        CHECK (access IN ('legacy_public', 'public'))
);

COMMENT ON TABLE sandbox_preview_policy IS
    'Preview routing policy. No row means legacy all-port routing; public '
    'rows route only explicitly published ports.';

CREATE TABLE sandbox_published_port (
    sandbox_id uuid NOT NULL REFERENCES sandbox (id) ON DELETE CASCADE,
    port integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (sandbox_id, port),
    CONSTRAINT sandbox_published_port_range CHECK (port >= 1024 AND port <= 65535)
);

COMMENT ON TABLE sandbox_published_port IS
    'Public preview ports explicitly routable for a strict sandbox.';

-- Kept outside host so adding the migration cannot change the result shape of
-- SELECT */RETURNING * queries embedded in an older rolling control plane.
CREATE TABLE host_capability (
    host_id text NOT NULL REFERENCES host (id) ON DELETE CASCADE,
    capability text NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (host_id, capability),
    CONSTRAINT host_capability_value_valid
        CHECK (capability <> '' AND octet_length(capability) <= 64)
);

COMMENT ON TABLE host_capability IS
    'Data-plane capabilities jointly advertised by the currently running host services. '
    'heartbeat_at must match host.last_heartbeat_at, so an old control-plane '
    'heartbeat automatically invalidates an attestation it cannot replace.';
