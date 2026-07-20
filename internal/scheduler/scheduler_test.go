package scheduler

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/superserve-ai/sandbox/internal/db"
	"github.com/superserve-ai/sandbox/internal/preview"
)

type queryCapture struct {
	sql string
}

func (c *queryCapture) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec")
}

func (c *queryCapture) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	c.sql = sql
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
	got, err := s.SelectHost(context.Background())
	if err != nil {
		t.Fatalf("SelectHost: %v", err)
	}
	if got != "fallback" {
		t.Fatalf("host = %q, want fallback", got)
	}
	if !strings.Contains(capture.sql, "FROM host_capability") ||
		!strings.Contains(capture.sql, "hc.capability = '"+preview.HostCapabilityPorts+"'") ||
		!strings.Contains(capture.sql, "hc.heartbeat_at = h.last_heartbeat_at") {
		t.Fatalf("scheduler query does not require %q capability:\n%s", preview.HostCapabilityPorts, capture.sql)
	}
}
