package api

import (
	"strings"
	"testing"
)

func TestParsePlatformBillingParams(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		got, err := parsePlatformBillingParams(pageCtx(t, ""))
		if err != nil {
			t.Fatalf("parse defaults: %v", err)
		}
		if got.Limit != platformBillingDefaultLimit || got.Offset != 0 {
			t.Fatalf("pagination = %d/%d, want %d/0", got.Limit, got.Offset, platformBillingDefaultLimit)
		}
		if got.SortBy != "team_name" || got.SortDir != "desc" {
			t.Fatalf("sort = %s/%s, want team_name/desc", got.SortBy, got.SortDir)
		}
	})

	t.Run("all inputs", func(t *testing.T) {
		got, err := parsePlatformBillingParams(pageCtx(t, "limit=25&offset=50&sort=current_charges_usd&order=asc&search=50%25_acme"))
		if err != nil {
			t.Fatalf("parse inputs: %v", err)
		}
		if got.Limit != 25 || got.Offset != 50 {
			t.Fatalf("pagination = %d/%d, want 25/50", got.Limit, got.Offset)
		}
		if got.SortBy != "current_charges_usd" || got.SortDir != "asc" {
			t.Fatalf("sort = %s/%s", got.SortBy, got.SortDir)
		}
		if got.Search != `50\%\_acme` {
			t.Fatalf("search = %q, want escaped literal", got.Search)
		}
	})

	t.Run("search too long", func(t *testing.T) {
		if _, err := parsePlatformBillingParams(pageCtx(t, "search="+strings.Repeat("a", 201))); err == nil {
			t.Fatal("expected long search to be rejected")
		}
	})
}
