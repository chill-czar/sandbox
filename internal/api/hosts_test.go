package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/superserve-ai/sandbox/internal/db"
	"github.com/superserve-ai/sandbox/internal/preview"
)

func setupHostHeartbeatRouter(h *Handlers) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/internal/hosts/:host_id/heartbeat", h.HostHeartbeat)
	return r
}

func TestHostHeartbeatReplacesAdvertisedCapabilities(t *testing.T) {
	var gotHostID string
	var gotCapabilities []string
	mock := &mockDBTX{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if !strings.Contains(sql, "-- name: UpdateHostHeartbeat :one") {
				return errorRow(fmt.Errorf("unexpected QueryRow: %s", sql))
			}
			gotHostID = args[0].(string)
			gotCapabilities = append([]string(nil), args[1].([]string)...)
			return hostRow(db.Host{ID: gotHostID, Status: "active", Capabilities: gotCapabilities})
		},
		execFn: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec: %s", sql)
		},
	}
	h := &Handlers{DB: db.New(mock)}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/hosts/host-a/heartbeat", strings.NewReader(`{"capabilities":["preview_ports_v1"]}`))
	setupHostHeartbeatRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if gotHostID != "host-a" {
		t.Fatalf("host id = %q, want host-a", gotHostID)
	}
	if len(gotCapabilities) != 1 || gotCapabilities[0] != preview.HostCapabilityPorts {
		t.Fatalf("capabilities = %#v, want [%q]", gotCapabilities, preview.HostCapabilityPorts)
	}
}

func TestHostHeartbeatWithoutBodyClearsCapabilities(t *testing.T) {
	called := false
	mock := &mockDBTX{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if !strings.Contains(sql, "-- name: UpdateHostHeartbeat :one") {
				return errorRow(fmt.Errorf("unexpected QueryRow: %s", sql))
			}
			called = true
			capabilities := args[1].([]string)
			if len(capabilities) != 0 {
				t.Errorf("capabilities = %#v, want empty replacement", capabilities)
			}
			return hostRow(db.Host{ID: args[0].(string), Status: "active"})
		},
		execFn: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec: %s", sql)
		},
	}
	h := &Handlers{DB: db.New(mock)}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/hosts/host-old/heartbeat", nil)
	setupHostHeartbeatRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK || !called {
		t.Fatalf("status = %d called = %v, want 200/true; body: %s", w.Code, called, w.Body.String())
	}
}

func TestHostHeartbeatRejectsMalformedCapabilityBeforeDB(t *testing.T) {
	called := false
	mock := &mockDBTX{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			called = true
			return errorRow(fmt.Errorf("DB must not be called"))
		},
		execFn: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec: %s", sql)
		},
	}
	h := &Handlers{DB: db.New(mock)}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/hosts/host-a/heartbeat", strings.NewReader(`{"capabilities":[""]}`))
	setupHostHeartbeatRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("invalid heartbeat reached the database")
	}
}
