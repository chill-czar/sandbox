package vm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"

	"github.com/superserve-ai/sandbox/internal/preview"
)

func TestSendHeartbeatAdvertisesPreviewPortCapability(t *testing.T) {
	t.Helper()
	var got heartbeatRequest
	var gotPath, gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode heartbeat: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sendHeartbeat(context.Background(), server.Client(), server.URL+"/internal/hosts/host-a/heartbeat", "shared", zerolog.Nop())

	if gotPath != "/internal/hosts/host-a/heartbeat" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuthorization != "Bearer shared" {
		t.Fatalf("authorization = %q", gotAuthorization)
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0] != preview.HostCapabilityPorts {
		t.Fatalf("capabilities = %#v, want [%q]", got.Capabilities, preview.HostCapabilityPorts)
	}
}
