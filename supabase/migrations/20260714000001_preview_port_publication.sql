-- Phase 1: explicit publication for public preview ports.
--
-- Existing sandboxes retain the historical behavior where every listening
-- non-privileged port is routable. Sandboxes created after this migration are
-- strict by default: only rows in sandbox_published_port are routable.

ALTER TABLE sandbox ADD COLUMN preview_access text;

-- The column is nullable only inside this transaction. Rows visible before the
-- migration are compatibility sandboxes; future inserts default to strict.
UPDATE sandbox SET preview_access = 'legacy_public';

ALTER TABLE sandbox
    ALTER COLUMN preview_access SET DEFAULT 'public',
    ALTER COLUMN preview_access SET NOT NULL,
    ADD COLUMN preview_policy_revision bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT sandbox_preview_access_valid
        CHECK (preview_access IN ('legacy_public', 'public'));

COMMENT ON COLUMN sandbox.preview_access IS
    'Preview routing mode: legacy_public routes every listening port; public '
    'routes only explicitly published ports.';

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

-- The heartbeat replaces this list on every beat, so rolling a host back to a
-- build that does not enforce publication immediately removes its capability.
ALTER TABLE host
    ADD COLUMN capabilities text[] NOT NULL DEFAULT '{}';

COMMENT ON COLUMN host.capabilities IS
    'Data-plane capabilities advertised by the currently running vmd/proxy build.';
