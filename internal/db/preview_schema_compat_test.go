package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPreviewPublicationMigrationPreservesRollingScannerShapes(t *testing.T) {
	path := filepath.Join("..", "..", "supabase", "migrations", "20260721000001_preview_port_publication.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(raw)
	for _, table := range []string{"sandbox_preview_policy", "sandbox_published_port", "host_capability"} {
		if !strings.Contains(sql, "CREATE TABLE "+table) {
			t.Errorf("migration does not create %s side table", table)
		}
		if !strings.Contains(sql, "ALTER TABLE public."+table+" ENABLE ROW LEVEL SECURITY") {
			t.Errorf("migration does not protect %s with row-level security", table)
		}
	}
	for _, existingTable := range []string{"sandbox", "host"} {
		pattern := regexp.MustCompile(`(?is)ALTER\s+TABLE\s+(?:public\.)?` + existingTable + `\b`)
		if pattern.MatchString(sql) {
			t.Errorf("migration alters existing %s row shape, which breaks old SELECT */RETURNING * scanners", existingTable)
		}
	}
}

func TestMissingPreviewPolicyRowRemainsLegacyPublic(t *testing.T) {
	path := filepath.Join("..", "..", "db", "queries", "sandboxes.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sandbox queries: %v", err)
	}
	queries := string(raw)
	if !strings.Contains(queries, "LEFT JOIN sandbox_preview_policy") ||
		!strings.Contains(queries, "COALESCE(p.access, 'legacy_public')") {
		t.Fatal("effective preview policy no longer maps an absent old-writer row to legacy_public")
	}
}

func TestHostCapabilityAttestationIsBoundToCurrentHeartbeat(t *testing.T) {
	path := filepath.Join("..", "..", "db", "queries", "hosts.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read host queries: %v", err)
	}
	queries := string(raw)
	if !strings.Contains(queries, "SELECT id, sqlc.arg(capability), last_heartbeat_at") {
		t.Fatal("capability insert is not stamped with the heartbeat it attests")
	}
	if got := strings.Count(queries, "hc.heartbeat_at = h.last_heartbeat_at"); got < 2 {
		t.Fatalf("current-heartbeat attestation checks = %d, want both host gate and scheduler", got)
	}
	if !strings.Contains(queries, "h.status = 'active'") {
		t.Fatal("host capability gate accepts capabilities from an unhealthy host")
	}
}
