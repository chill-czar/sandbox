package scheduler

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/superserve-ai/sandbox/internal/db"
	"github.com/superserve-ai/sandbox/internal/preview"
)

type queryCapture struct {
	sql   string
	args  [][]string
	calls int
}

func (c *queryCapture) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec")
}

func (c *queryCapture) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.sql = sql
	c.calls++
	c.args = append(c.args, append([]string(nil), args[0].([]string)...))
	return schedulerEmptyRows{}, nil
}

func (c *queryCapture) QueryRow(context.Context, string, ...any) pgx.Row {
	return schedulerErrorRow{err: fmt.Errorf("unexpected QueryRow")}
}

type schedulerErrorRow struct{ err error }

func (r schedulerErrorRow) Scan(...any) error { return r.err }

type schedulerEmptyRows struct{}

func (schedulerEmptyRows) Close()                                       {}
func (schedulerEmptyRows) Err() error                                   { return nil }
func (schedulerEmptyRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (schedulerEmptyRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (schedulerEmptyRows) Next() bool                                   { return false }
func (schedulerEmptyRows) Scan(...any) error                            { return fmt.Errorf("no row") }
func (schedulerEmptyRows) Values() ([]any, error)                       { return nil, nil }
func (schedulerEmptyRows) RawValues() [][]byte                          { return nil }
func (schedulerEmptyRows) Conn() *pgx.Conn                              { return nil }

func TestLeastLoadedQueryExcludesHostsWithoutPreviewEnforcement(t *testing.T) {
	capture := &queryCapture{}
	s := &LeastLoaded{DB: db.New(capture), DefaultHostID: "fallback"}
	required := []string{preview.HostCapabilityPorts}
	got, err := s.SelectHost(context.Background(), required)
	if err != nil {
		t.Fatalf("SelectHost: %v", err)
	}
	if got != "fallback" {
		t.Fatalf("host = %q, want fallback", got)
	}
	if !strings.Contains(capture.sql, "FROM host_capability") ||
		!strings.Contains(capture.sql, "FROM unnest(") ||
		strings.Count(capture.sql, "NOT EXISTS") < 2 ||
		!strings.Contains(capture.sql, "hc.heartbeat_at = h.last_heartbeat_at") {
		t.Fatalf("scheduler query does not require all current-heartbeat capabilities:\n%s", capture.sql)
	}
	if !reflect.DeepEqual(capture.args, [][]string{required}) {
		t.Fatalf("query capabilities = %#v, want %#v", capture.args, [][]string{required})
	}
}

func TestLeastLoadedCachesCandidateSetsByCanonicalCapabilities(t *testing.T) {
	capture := &queryCapture{}
	s := &LeastLoaded{DB: db.New(capture), DefaultHostID: "fallback"}
	ctx := context.Background()

	public := []string{preview.HostCapabilityPorts}
	private := []string{preview.HostCapabilityPortTokens, preview.HostCapabilityPortAccess, preview.HostCapabilityPorts}
	for _, required := range [][]string{public, public, private, {
		preview.HostCapabilityPorts, preview.HostCapabilityPortTokens, preview.HostCapabilityPortAccess,
		preview.HostCapabilityPorts, preview.HostCapabilityPortTokens,
	}} {
		if got, err := s.SelectHost(ctx, required); err != nil || got != "fallback" {
			t.Fatalf("SelectHost(%v) = (%q, %v), want fallback", required, got, err)
		}
	}

	if capture.calls != 2 {
		t.Fatalf("query calls = %d, want 2 capability-specific cache fills", capture.calls)
	}
	wantPrivate := []string{
		preview.HostCapabilityPortAccess,
		preview.HostCapabilityPortTokens,
		preview.HostCapabilityPorts,
	}
	if !reflect.DeepEqual(capture.args[1], wantPrivate) {
		t.Fatalf("private requirements = %#v, want %#v", capture.args[1], wantPrivate)
	}
}
