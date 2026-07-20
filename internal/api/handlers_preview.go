package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/superserve-ai/sandbox/internal/db"
	"github.com/superserve-ai/sandbox/internal/preview"
)

const (
	minPreviewPort = 1024
	maxPreviewPort = 65535
)

func validatePreviewAccess(value *string) error {
	if value == nil || *value == preview.AccessPublic {
		return nil
	}
	return fmt.Errorf("preview_access must be %q", preview.AccessPublic)
}

func (h *Handlers) loadPreviewPorts(ctx context.Context, sandboxID uuid.UUID) (map[int32]struct{}, error) {
	rows, err := h.DB.ListPublishedPorts(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return publishedPortSet(rows), nil
}

func publishedPortSet(ports []int32) map[int32]struct{} {
	if len(ports) == 0 {
		return nil
	}
	out := make(map[int32]struct{}, len(ports))
	for _, port := range ports {
		out[port] = struct{}{}
	}
	return out
}

// applyPreviewMutation serializes publication changes on the sandbox row,
// performs the mutation, and reads the resulting full allowlist in the same
// transaction. The returned revision orders the later vmd pushes.
func (h *Handlers) applyPreviewMutation(ctx context.Context, sandboxID, teamID uuid.UUID, mutate func(*db.Queries) error) (int64, map[int32]struct{}, error) {
	run := func(q *db.Queries) (int64, map[int32]struct{}, error) {
		revision, err := q.BumpPreviewPolicyRevision(ctx, db.BumpPreviewPolicyRevisionParams{ID: sandboxID, TeamID: teamID})
		if err != nil {
			if err == pgx.ErrNoRows {
				return 0, nil, ErrSandboxNotFound
			}
			return 0, nil, err
		}
		if err := mutate(q); err != nil {
			return 0, nil, err
		}
		ports, err := q.ListPublishedPorts(ctx, sandboxID)
		if err != nil {
			return 0, nil, err
		}
		return revision, publishedPortSet(ports), nil
	}

	if h.Pool == nil {
		return run(h.DB)
	}
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx)
	revision, ports, err := run(h.DB.WithTx(tx))
	if err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	return revision, ports, nil
}

func (h *Handlers) pushPreviewPolicy(ctx context.Context, sandbox db.Sandbox, ports map[int32]struct{}, revision int64) error {
	vmd, err := h.vmdForHost(ctx, sandbox.HostID)
	if err != nil {
		return fmt.Errorf("resolve vmd: %w", err)
	}
	vmdCtx, cancel := context.WithTimeout(ctx, vmdTimeout)
	defer cancel()
	err = vmd.UpdateSandboxPreviewPolicy(vmdCtx, sandbox.ID.String(), sandbox.PreviewAccess, ports, revision)
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}

func (h *Handlers) requireHostPreviewPorts(c *gin.Context, hostID string) bool {
	host, err := h.DB.GetHost(c.Request.Context(), hostID)
	if err != nil && err != pgx.ErrNoRows {
		log.Error().Err(err).Str("host_id", hostID).Msg("DB GetHost failed")
		respondError(c, ErrInternal)
		return false
	}
	if err == pgx.ErrNoRows || !slices.Contains(host.Capabilities, preview.HostCapabilityPorts) {
		respondErrorMsg(c, "conflict",
			fmt.Sprintf("The sandbox's host does not enforce %q; retry after the fleet is upgraded", preview.HostCapabilityPorts),
			http.StatusConflict)
		return false
	}
	return true
}

func parsePreviewPort(c *gin.Context) (int32, bool) {
	port, err := strconv.Atoi(c.Param("port"))
	if err != nil || port < minPreviewPort || port > maxPreviewPort {
		respondErrorMsg(c, "bad_request", fmt.Sprintf("port must be an integer in [%d, %d]", minPreviewPort, maxPreviewPort), http.StatusBadRequest)
		return 0, false
	}
	return int32(port), true
}

type publishedPortResponse struct {
	Port int32 `json:"port"`
}

type listPreviewPortsResponse struct {
	PreviewAccess string                  `json:"preview_access"`
	Ports         []publishedPortResponse `json:"ports"`
}

func (h *Handlers) ListSandboxPreviewPorts(c *gin.Context) {
	sandboxID, err := parseSandboxID(c)
	if err != nil {
		return
	}
	teamID, err := teamIDFromContext(c)
	if err != nil {
		return
	}
	if !h.requireTeamSandboxRead(c, teamID) {
		return
	}
	sandbox, ok := h.getTeamSandbox(c, sandboxID, teamID)
	if !ok {
		return
	}
	rows, err := h.DB.ListPublishedPorts(c.Request.Context(), sandboxID)
	if err != nil {
		log.Error().Err(err).Str("sandbox_id", sandboxID.String()).Msg("DB ListPublishedPorts failed")
		respondError(c, ErrInternal)
		return
	}
	ports := make([]publishedPortResponse, 0, len(rows))
	for _, port := range rows {
		ports = append(ports, publishedPortResponse{Port: port})
	}
	c.JSON(http.StatusOK, listPreviewPortsResponse{PreviewAccess: sandbox.PreviewAccess, Ports: ports})
}

type publishPortRequest struct {
	Port int32 `json:"port"`
}

func (h *Handlers) PublishSandboxPreviewPort(c *gin.Context) {
	sandboxID, err := parseSandboxID(c)
	if err != nil {
		return
	}
	teamID, err := teamIDFromContext(c)
	if err != nil {
		return
	}
	if !h.requireTeamSandboxWrite(c, teamID) {
		return
	}
	var body publishPortRequest
	if err := bindJSONStrict(c, &body); err != nil {
		respondErrorMsg(c, "bad_request", "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Port < minPreviewPort || body.Port > maxPreviewPort {
		respondErrorMsg(c, "bad_request", fmt.Sprintf("port must be an integer in [%d, %d]", minPreviewPort, maxPreviewPort), http.StatusBadRequest)
		return
	}
	sandbox, ok := h.getTeamSandbox(c, sandboxID, teamID)
	if !ok || !h.requireHostPreviewPorts(c, sandbox.HostID) {
		return
	}

	var port int32
	revision, ports, err := h.applyPreviewMutation(c.Request.Context(), sandboxID, teamID, func(q *db.Queries) error {
		var mutationErr error
		port, mutationErr = q.PublishPort(c.Request.Context(), db.PublishPortParams{SandboxID: sandboxID, Port: body.Port})
		return mutationErr
	})
	if !h.handlePreviewMutationResult(c, sandboxID, "PublishPort", err) || !h.pushAfterPreviewMutation(c, sandbox, ports, revision) {
		return
	}

	detail, _ := json.Marshal(map[string]any{"port": body.Port})
	h.logSandboxActivity(c.Request.Context(), sandbox.ID, teamID, actorIDFromContext(c), "sandbox", "preview_port_published", "success", &sandbox.Name, nil, detail)
	h.capture(c, "sandbox_preview_port_published", map[string]any{"sandbox_id": sandboxID.String(), "port": body.Port})
	c.JSON(http.StatusOK, publishedPortResponse{Port: port})
}

// Unpublish is idempotent and retry-safe. Even when the row is already absent,
// it advances the revision and re-pushes the authoritative allowlist so a
// failed earlier attempt can always be repaired by repeating DELETE.
func (h *Handlers) UnpublishSandboxPreviewPort(c *gin.Context) {
	sandboxID, err := parseSandboxID(c)
	if err != nil {
		return
	}
	port, ok := parsePreviewPort(c)
	if !ok {
		return
	}
	teamID, err := teamIDFromContext(c)
	if err != nil {
		return
	}
	if !h.requireTeamSandboxWrite(c, teamID) {
		return
	}
	sandbox, ok := h.getTeamSandbox(c, sandboxID, teamID)
	if !ok {
		return
	}

	var deleted int64
	revision, ports, err := h.applyPreviewMutation(c.Request.Context(), sandboxID, teamID, func(q *db.Queries) error {
		var mutationErr error
		deleted, mutationErr = q.UnpublishPort(c.Request.Context(), db.UnpublishPortParams{SandboxID: sandboxID, Port: port})
		return mutationErr
	})
	if !h.handlePreviewMutationResult(c, sandboxID, "UnpublishPort", err) || !h.pushAfterPreviewMutation(c, sandbox, ports, revision) {
		return
	}
	if deleted > 0 {
		detail, _ := json.Marshal(map[string]any{"port": port})
		h.logSandboxActivity(c.Request.Context(), sandbox.ID, teamID, actorIDFromContext(c), "sandbox", "preview_port_unpublished", "success", &sandbox.Name, nil, detail)
		h.capture(c, "sandbox_preview_port_unpublished", map[string]any{"sandbox_id": sandboxID.String(), "port": port})
	}
	c.Status(http.StatusNoContent)
}

func (h *Handlers) handlePreviewMutationResult(c *gin.Context, sandboxID uuid.UUID, operation string, err error) bool {
	if err == nil {
		return true
	}
	if err == ErrSandboxNotFound {
		respondError(c, ErrSandboxNotFound)
		return false
	}
	log.Error().Err(err).Str("sandbox_id", sandboxID.String()).Str("operation", operation).Msg("preview mutation failed")
	respondError(c, ErrInternal)
	return false
}

func (h *Handlers) pushAfterPreviewMutation(c *gin.Context, sandbox db.Sandbox, ports map[int32]struct{}, revision int64) bool {
	if err := h.pushPreviewPolicy(c.Request.Context(), sandbox, ports, revision); err != nil {
		log.Error().Err(err).Str("sandbox_id", sandbox.ID.String()).Msg("push preview policy failed; retry to converge")
		respondError(c, ErrInternal)
		return false
	}
	return true
}

func (h *Handlers) getTeamSandbox(c *gin.Context, sandboxID, teamID uuid.UUID) (db.Sandbox, bool) {
	sandbox, err := h.DB.GetSandbox(c.Request.Context(), db.GetSandboxParams{ID: sandboxID, TeamID: teamID})
	if err == pgx.ErrNoRows {
		respondError(c, ErrSandboxNotFound)
		return db.Sandbox{}, false
	}
	if err != nil {
		log.Error().Err(err).Str("sandbox_id", sandboxID.String()).Msg("DB GetSandbox failed")
		respondError(c, ErrInternal)
		return db.Sandbox{}, false
	}
	return sandbox, true
}
