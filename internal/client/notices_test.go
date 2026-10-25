package client

import (
	"context"
	"net/http"
	"testing"
)

func TestNoticesCollectDeprecationHeaders(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/old":
			w.Header().Set("Deprecation", "@1767225600")
			w.Header().Set("Sunset", "Sat, 01 Aug 2026 00:00:00 GMT")
			w.Header().Add("Link", `<https://docs.routeur.ai/api/deprecations>; rel="deprecation", <https://example.com/x>; rel="next"`)
		case "/api/v1/gone-soon":
			w.Header().Set("Sunset", "Tue, 01 Sep 2026 00:00:00 GMT")
		}
		writeJSON(w, 200, map[string]any{"data": thing{}})
	})

	// Without a collector, nothing is recorded and nothing breaks.
	if err := c.Do(context.Background(), http.MethodGet, "/old", nil, nil); err != nil {
		t.Fatal(err)
	}

	ctx, notices := WithNotices(context.Background())
	for _, p := range []string{"/old?cursor=a", "/old?cursor=b", "/fine", "/gone-soon"} {
		if err := c.Do(ctx, http.MethodGet, p, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	got := notices.List()
	if len(got) != 2 {
		t.Fatalf("notices = %+v, want 2 (the same headers twice are one notice)", got)
	}
	if got[0].Method != http.MethodGet || got[0].Path != "/old" || got[0].Sunset != "Sat, 01 Aug 2026 00:00:00 GMT" {
		t.Errorf("first notice = %+v", got[0])
	}
	if got[0].Since() != "2026-01-01T00:00:00Z" {
		t.Errorf("Since() = %q", got[0].Since())
	}
	if len(got[0].Links) != 1 || got[0].Links[0] != `<https://docs.routeur.ai/api/deprecations>; rel="deprecation"` {
		t.Errorf("links = %q", got[0].Links)
	}
	if got[1].Path != "/gone-soon" || got[1].Deprecation != "" {
		t.Errorf("second notice = %+v", got[1])
	}
}

// Error responses carry the headers too (a deprecated endpoint refusing a
// request is still deprecated).
func TestNoticesOnErrorResponses(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Deprecation", "true")
		writeJSON(w, 422, map[string]any{"message": "no", "code": "validation_failed"})
	})
	ctx, notices := WithNotices(context.Background())
	_ = c.Do(ctx, http.MethodPost, "/x", map[string]any{}, nil)
	if got := notices.List(); len(got) != 1 || got[0].Since() != "true" {
		t.Errorf("notices = %+v", got)
	}
}
