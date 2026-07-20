package vm

import (
	"path/filepath"
	"testing"

	"github.com/superserve-ai/sandbox/internal/preview"
)

func TestPreviewPolicyRecordRoundTrip(t *testing.T) {
	inst := &VMInstance{
		ID:                    "vm-preview",
		PreviewAccess:         preview.AccessPublic,
		PreviewPorts:          map[int32]struct{}{3000: {}, 8080: {}},
		PreviewPolicyRevision: 7,
	}

	rec := toRecord(inst)
	if rec.PreviewAccess != preview.AccessPublic || rec.PreviewPolicyRevision != 7 {
		t.Fatalf("record policy = (%q, %d), want (%q, 7)", rec.PreviewAccess, rec.PreviewPolicyRevision, preview.AccessPublic)
	}
	if !rec.PreviewPorts[3000] || !rec.PreviewPorts[8080] {
		t.Fatalf("record ports = %#v, want 3000 and 8080", rec.PreviewPorts)
	}

	restored := toInstance(rec)
	if restored.PreviewAccess != preview.AccessPublic || restored.PreviewPolicyRevision != 7 {
		t.Fatalf("restored policy = (%q, %d), want (%q, 7)", restored.PreviewAccess, restored.PreviewPolicyRevision, preview.AccessPublic)
	}
	if _, ok := restored.PreviewPorts[3000]; !ok {
		t.Fatalf("restored ports = %#v, want 3000", restored.PreviewPorts)
	}
	if _, ok := restored.PreviewPorts[8080]; !ok {
		t.Fatalf("restored ports = %#v, want 8080", restored.PreviewPorts)
	}

	// Conversion must not alias either mutable map. A later policy update in
	// memory cannot mutate the durable record that was already constructed.
	delete(restored.PreviewPorts, 3000)
	if !rec.PreviewPorts[3000] {
		t.Fatal("restored preview ports alias the persisted record")
	}
}

func TestLegacyVMRecordRetainsAllPortCompatibility(t *testing.T) {
	// Records written before preview publication have none of the new JSON
	// fields. Their zero values intentionally retain the legacy routing mode.
	inst := toInstance(VMRecord{ID: "vm-legacy"})
	if inst.PreviewAccess != "" {
		t.Fatalf("preview access = %q, want legacy zero value", inst.PreviewAccess)
	}
	if inst.PreviewPorts != nil || inst.PreviewPolicyRevision != 0 {
		t.Fatalf("legacy policy = (%#v, %d), want (nil, 0)", inst.PreviewPorts, inst.PreviewPolicyRevision)
	}
}

func TestUpdateSandboxPreviewPolicyRejectsStaleRevision(t *testing.T) {
	inst := &VMInstance{
		ID:                    "vm-preview",
		PreviewAccess:         preview.AccessPublic,
		PreviewPorts:          map[int32]struct{}{3000: {}},
		PreviewPolicyRevision: 2,
	}
	mgr := &Manager{vms: map[string]*VMInstance{inst.ID: inst}}

	if err := mgr.UpdateSandboxPreviewPolicy(inst.ID, preview.AccessPublic, map[int32]struct{}{4000: {}}, 1); err != nil {
		t.Fatalf("stale update: %v", err)
	}
	if _, ok := inst.PreviewPorts[3000]; !ok {
		t.Fatalf("stale revision replaced policy: %#v", inst.PreviewPorts)
	}
	if _, ok := inst.PreviewPorts[4000]; ok {
		t.Fatalf("stale revision opened port 4000: %#v", inst.PreviewPorts)
	}

	incoming := map[int32]struct{}{5000: {}}
	if err := mgr.UpdateSandboxPreviewPolicy(inst.ID, preview.AccessPublic, incoming, 3); err != nil {
		t.Fatalf("new update: %v", err)
	}
	if inst.PreviewPolicyRevision != 3 {
		t.Fatalf("revision = %d, want 3", inst.PreviewPolicyRevision)
	}
	if _, ok := inst.PreviewPorts[5000]; !ok {
		t.Fatalf("new revision not applied: %#v", inst.PreviewPorts)
	}
	delete(incoming, 5000)
	if _, ok := inst.PreviewPorts[5000]; !ok {
		t.Fatal("manager retained caller-owned preview port map")
	}
}

func TestUpdateSandboxPreviewPolicyPersistenceFailureLeavesRevisionRetryable(t *testing.T) {
	store, err := OpenStateStore(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close state store: %v", err)
	}

	inst := &VMInstance{
		ID:                    "vm-preview",
		PreviewAccess:         preview.AccessPublic,
		PreviewPorts:          map[int32]struct{}{3000: {}},
		PreviewPolicyRevision: 4,
	}
	mgr := &Manager{
		vms:   map[string]*VMInstance{inst.ID: inst},
		state: store,
	}

	err = mgr.UpdateSandboxPreviewPolicy(inst.ID, preview.AccessPublic, map[int32]struct{}{8080: {}}, 5)
	if err == nil {
		t.Fatal("update succeeded with a closed state store")
	}
	if inst.PreviewPolicyRevision != 4 {
		t.Fatalf("revision = %d, want retryable revision 4", inst.PreviewPolicyRevision)
	}
	if _, ok := inst.PreviewPorts[3000]; !ok {
		t.Fatalf("failed persistence changed current ports: %#v", inst.PreviewPorts)
	}
	if _, ok := inst.PreviewPorts[8080]; ok {
		t.Fatalf("failed persistence applied new port: %#v", inst.PreviewPorts)
	}
}

// TestPutIfPresent pins the atomic conditional write the background reattach
// relies on: it must write when the record exists and be a no-op (never
// resurrect) once the record has been deleted.
func TestPutIfPresent(t *testing.T) {
	s, err := OpenStateStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	rec := VMRecord{ID: "vm-1", Status: StatusRunning}

	// Absent key → no write.
	if wrote, err := s.PutIfPresent(rec); err != nil || wrote {
		t.Fatalf("PutIfPresent on absent key = (%v, %v), want (false, nil)", wrote, err)
	}
	if has, _ := s.Has("vm-1"); has {
		t.Fatal("record must not exist after PutIfPresent on an absent key")
	}

	// Present key → writes (and updates).
	if err := s.Put(rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rec.Status = StatusPaused
	if wrote, err := s.PutIfPresent(rec); err != nil || !wrote {
		t.Fatalf("PutIfPresent on present key = (%v, %v), want (true, nil)", wrote, err)
	}
	if got, _ := s.Get("vm-1"); got == nil || got.Status != StatusPaused {
		t.Fatalf("record not updated: %+v", got)
	}

	// Deleted key → must NOT resurrect (the whole point of the fix).
	if err := s.Delete("vm-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if wrote, err := s.PutIfPresent(rec); err != nil || wrote {
		t.Fatalf("PutIfPresent after delete = (%v, %v), want (false, nil)", wrote, err)
	}
	if has, _ := s.Has("vm-1"); has {
		t.Fatal("record resurrected after delete — PutIfPresent must be a no-op")
	}
}
