//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

type platformBillingTestResponse struct {
	Rows []struct {
		TeamID   string         `json:"team_id"`
		TeamName string         `json:"team_name"`
		Summary  map[string]any `json:"summary"`
		Error    *struct {
			Code string `json:"code"`
		} `json:"error"`
	} `json:"rows"`
	Pagination struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
		Total  int `json:"total"`
	} `json:"pagination"`
	Totals struct {
		Teams     int     `json:"teams"`
		Succeeded int     `json:"succeeded"`
		Failed    int     `json:"failed"`
		Charges   float64 `json:"current_charges_usd"`
	} `json:"totals"`
}

func decodePlatformBilling(t *testing.T, body []byte) platformBillingTestResponse {
	t.Helper()
	var response platformBillingTestResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode platform billing response: %v\n%s", err, body)
	}
	return response
}

func TestPlatformBillingPaginationTotalsAndPartialFailures(t *testing.T) {
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	prefix := "platform-billing-" + suffix
	goodTeam, err := testQueries.CreateTeam(ctx, prefix+"-alpha")
	if err != nil {
		t.Fatalf("create team with valid pricing: %v", err)
	}
	badTeam, err := testQueries.CreateTeam(ctx, prefix+"-beta")
	if err != nil {
		t.Fatalf("create team with incomplete pricing: %v", err)
	}

	planKey := "incomplete-" + suffix
	if _, err := testPool.Exec(ctx, `
		INSERT INTO pricing_plan (key, name, currency)
		VALUES ($1, 'Incomplete test plan', 'USD')
	`, planKey); err != nil {
		t.Fatalf("seed incomplete pricing plan: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO pricing_rate (plan_key, resource, unit, price_usd)
		VALUES
			($1, 'vcpu', 'second', 0.00001),
			($1, 'memory_gib', 'second', 0.00001)
	`, planKey); err != nil {
		t.Fatalf("seed incomplete pricing rates: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO team_pricing_plan (team_id, plan_key)
		VALUES ($2, $1)
	`, planKey, badTeam.ID); err != nil {
		t.Fatalf("assign incomplete pricing plan: %v", err)
	}

	actorID := seedPlatformAdminProfile(t)
	r := newInternalRouter(t)

	first := doInternal(
		r,
		http.MethodGet,
		fmt.Sprintf("/internal/billing?search=%s&sort=team_name&order=asc&limit=1", prefix),
		actorID.String(),
		"",
	)
	if first.Code != http.StatusOK {
		t.Fatalf("first page: %d %s", first.Code, first.Body.String())
	}
	firstBody := decodePlatformBilling(t, first.Body.Bytes())
	if firstBody.Pagination.Total != 2 || firstBody.Totals.Teams != 2 {
		t.Fatalf("page-independent totals = pagination:%d totals:%d, want 2/2", firstBody.Pagination.Total, firstBody.Totals.Teams)
	}
	if firstBody.Totals.Succeeded != 1 || firstBody.Totals.Failed != 1 {
		t.Fatalf("success/failure totals = %d/%d, want 1/1", firstBody.Totals.Succeeded, firstBody.Totals.Failed)
	}
	if len(firstBody.Rows) != 1 || firstBody.Rows[0].TeamID != goodTeam.ID.String() {
		t.Fatalf("first page rows = %+v, want valid-pricing team", firstBody.Rows)
	}
	if firstBody.Rows[0].Summary == nil || firstBody.Rows[0].Error != nil {
		t.Fatalf("valid-pricing row should contain a summary: %+v", firstBody.Rows[0])
	}

	second := doInternal(
		r,
		http.MethodGet,
		fmt.Sprintf("/internal/billing?search=%s&sort=team_name&order=asc&limit=1&offset=1", prefix),
		actorID.String(),
		"",
	)
	if second.Code != http.StatusOK {
		t.Fatalf("second page: %d %s", second.Code, second.Body.String())
	}
	secondBody := decodePlatformBilling(t, second.Body.Bytes())
	if len(secondBody.Rows) != 1 || secondBody.Rows[0].TeamID != badTeam.ID.String() {
		t.Fatalf("second page rows = %+v, want incomplete-pricing team", secondBody.Rows)
	}
	if secondBody.Rows[0].Summary != nil || secondBody.Rows[0].Error == nil || secondBody.Rows[0].Error.Code != "pricing_unavailable" {
		t.Fatalf("incomplete-pricing row should contain a partial failure: %+v", secondBody.Rows[0])
	}

	unprivileged := seedSuperserveEmailProfile(t)
	denied := doInternal(r, http.MethodGet, "/internal/billing?search="+prefix, unprivileged.String(), "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unprivileged platform billing status = %d, want 403: %s", denied.Code, denied.Body.String())
	}
}
