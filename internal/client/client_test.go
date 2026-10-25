package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type thing struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL + "/api/v1", Token: "rtm_secret", UserAgent: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	c.backoff = func(int) time.Duration { return time.Millisecond }
	return c
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("missing token: want error")
	}
	for _, bad := range []string{"app.routeur.ai/api/v1", "ftp://x/y", "://"} {
		if _, err := New(Config{Token: "t", BaseURL: bad}); err == nil {
			t.Errorf("base URL %q: want error", bad)
		}
	}
	c, err := New(Config{Token: "t"})
	if err != nil || c.baseURL != DefaultBaseURL {
		t.Errorf("default base URL: got %q, %v", c.baseURL, err)
	}
	c, _ = New(Config{Token: "t", BaseURL: "http://localhost:8080/api/v1/"})
	if c.baseURL != "http://localhost:8080/api/v1" {
		t.Errorf("trailing slash not trimmed: %q", c.baseURL)
	}
}

func TestDoSendsHeadersAndDecodes(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer rtm_secret" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "test-agent" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		if !strings.HasPrefix(r.Header.Get("X-Request-Id"), "tf-") {
			t.Errorf("X-Request-Id = %q", r.Header.Get("X-Request-Id"))
		}
		if r.Method == http.MethodPost {
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["name"] != "  verbatim " {
				t.Errorf("body not sent verbatim: %v", in)
			}
		}
		if r.URL.Path != "/api/v1/orgs/acme/workspaces" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeJSON(w, http.StatusCreated, map[string]any{"data": thing{ID: "ws_1", Name: "x"}})
	})
	got, err := Send[thing](context.Background(), c, http.MethodPost, Path("orgs", "acme", "workspaces"), map[string]any{"name": "  verbatim "})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "ws_1" {
		t.Errorf("got %+v", got)
	}
}

func TestDoNoContent(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	var out thing
	if err := c.Do(context.Background(), http.MethodDelete, "/x", nil, &out); err != nil {
		t.Fatal(err)
	}
}

func TestPathEscaping(t *testing.T) {
	if got := Path("orgs", "acme", "models", "google:models/gemini-pro"); got != "/orgs/acme/models/google:models%2Fgemini-pro" {
		t.Errorf("Path = %q", got)
	}
	var raw string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawPath
		if raw == "" {
			raw = r.URL.Path
		}
		writeJSON(w, 200, map[string]any{"data": thing{}})
	})
	if _, err := Get[thing](context.Background(), c, Path("orgs", "a b", "models", "google:models/gemini-pro")); err != nil {
		t.Fatal(err)
	}
	if raw != "/api/v1/orgs/a%20b/models/google:models%2Fgemini-pro" {
		t.Errorf("escaped path on the wire = %q", raw)
	}
}

func TestErrorEnvelope(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 422, map[string]any{
			"message": "The given data was invalid.",
			"code":    "validation_failed",
			"errors":  map[string][]string{"slug": {"The slug has already been taken."}, "name": {"Too long."}},
		})
	})
	err := c.Do(context.Background(), http.MethodPost, "/x", map[string]any{}, nil)
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("want *APIError, got %T %v", err, err)
	}
	if apiErr.Status != 422 || apiErr.Code != "validation_failed" || len(apiErr.Fields["slug"]) != 1 {
		t.Errorf("decoded %+v", apiErr)
	}
	if !strings.HasPrefix(apiErr.RequestID, "tf-") {
		t.Errorf("RequestID = %q", apiErr.RequestID)
	}
	want := "The given data was invalid. (HTTP 422, code validation_failed)\n  name: Too long.\n  slug: The slug has already been taken."
	if apiErr.Error() != want {
		t.Errorf("Error() =\n%s\nwant\n%s", apiErr.Error(), want)
	}
	if IsNotFound(err) {
		t.Error("IsNotFound on a 422")
	}
}

func TestFeatureUnavailable(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 403, map[string]any{"message": "Upgrade.", "code": "feature_unavailable", "feature": "budgets", "required_plan": "team"})
	})
	err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil)
	if !strings.Contains(err.Error(), `the "budgets" feature needs the "team" plan`) {
		t.Errorf("Error() = %q", err)
	}
}

func TestNonEnvelopeError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "<html>bad gateway config</html>")
	})
	err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil)
	apiErr, _ := AsAPIError(err)
	if apiErr == nil || apiErr.Status != 400 || !strings.Contains(apiErr.Message, "Bad Request: <html>") {
		t.Errorf("got %v", err)
	}
}

func TestIsNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]any{"message": "Not found.", "code": "not_found"})
	})
	err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil)
	if !IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false", err)
	}
	if IsNotFound(errors.New("plain")) || IsNotFound(nil) {
		t.Error("IsNotFound true for a non-API error")
	}
	if !IsNotFound(fmt.Errorf("wrapped: %w", err)) {
		t.Error("IsNotFound does not unwrap")
	}
}

func TestRetryPolicy(t *testing.T) {
	cases := []struct {
		method    string
		status    int
		wantCalls int32
	}{
		{http.MethodGet, 503, 5},
		{http.MethodPut, 502, 5},
		{http.MethodDelete, 504, 5},
		{http.MethodPost, 503, 1},
		{http.MethodPatch, 502, 1},
		{http.MethodGet, 500, 1},
		{http.MethodGet, 422, 1},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s_%d", tc.method, tc.status), func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				writeJSON(w, tc.status, map[string]any{"message": "no", "code": "x"})
			})
			err := c.Do(context.Background(), tc.method, "/x", nil, nil)
			if apiErr, ok := AsAPIError(err); !ok || apiErr.Status != tc.status {
				t.Errorf("err = %v", err)
			}
			if got := calls.Load(); got != tc.wantCalls {
				t.Errorf("calls = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}

// 429s are retried for a time budget, not an attempt count, for every
// method.
func TestRateLimitRetriesForTimeBudget(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				writeJSON(w, 429, map[string]any{"message": "slow down", "code": "rate_limited"})
			})
			c.rateLimitBudget = 200 * time.Millisecond
			c.backoff = func(int) time.Duration { return 10 * time.Millisecond }
			start := time.Now()
			err := c.Do(context.Background(), method, "/x", nil, nil)
			elapsed := time.Since(start)
			if apiErr, ok := AsAPIError(err); !ok || apiErr.Code != "rate_limited" {
				t.Errorf("err = %v", err)
			}
			if got := calls.Load(); got <= int32(defaultMaxAttempts) {
				t.Errorf("calls = %d, want more than the %d-attempt cap for 5xx", got, defaultMaxAttempts)
			}
			if elapsed < 150*time.Millisecond || elapsed > 2*time.Second {
				t.Errorf("gave up after %s, want about the 200ms budget", elapsed)
			}
		})
	}
}

// The default budget is minutes, and a Retry-After that would overrun the
// remaining budget fails at once instead of sleeping past it.
func TestRateLimitBudget(t *testing.T) {
	c, err := New(Config{Token: "rtm_x"})
	if err != nil {
		t.Fatal(err)
	}
	if c.rateLimitBudget != 5*time.Minute {
		t.Errorf("default rate-limit budget = %s, want 5m", c.rateLimitBudget)
	}

	var calls atomic.Int32
	c = newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		writeJSON(w, 429, map[string]any{"message": "slow down", "code": "rate_limited"})
	})
	c.rateLimitBudget = 30 * time.Second
	start := time.Now()
	if apiErr, ok := AsAPIError(c.Do(context.Background(), http.MethodGet, "/x", nil, nil)); !ok || apiErr.Status != 429 {
		t.Error("want the 429")
	}
	if calls.Load() != 1 || time.Since(start) > time.Second {
		t.Errorf("calls = %d after %s; want one call and no wait", calls.Load(), time.Since(start))
	}
}

// 429s do not use up the attempts a 5xx gets.
func TestRateLimitAndServerErrorsCountedApart(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		switch {
		case n <= 6:
			writeJSON(w, 429, map[string]any{"message": "slow down", "code": "rate_limited"})
		case n <= 9:
			writeJSON(w, 503, map[string]any{"message": "busy", "code": "server_error"})
		default:
			writeJSON(w, 200, map[string]any{"data": thing{ID: "ok"}})
		}
	})
	got, err := Get[thing](context.Background(), c, "/x")
	if err != nil || got.ID != "ok" || calls.Load() != 10 {
		t.Errorf("got %+v, %v after %d calls", got, err, calls.Load())
	}
}

func TestRetryThenSucceed(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			writeJSON(w, 503, map[string]any{"message": "busy", "code": "server_error"})
			return
		}
		writeJSON(w, 200, map[string]any{"data": thing{ID: "ok"}})
	})
	got, err := Get[thing](context.Background(), c, "/x")
	if err != nil || got.ID != "ok" || calls.Load() != 3 {
		t.Errorf("got %+v, %v after %d calls", got, err, calls.Load())
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	var calls atomic.Int32
	var first time.Time
	var gap time.Duration
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			first = time.Now()
			w.Header().Set("Retry-After", "1")
			writeJSON(w, 429, map[string]any{"message": "slow down", "code": "rate_limited"})
			return
		}
		gap = time.Since(first)
		writeJSON(w, 200, map[string]any{"data": thing{}})
	})
	c.backoff = func(int) time.Duration { t.Error("backoff used despite Retry-After"); return 0 }
	if err := c.Do(context.Background(), http.MethodPost, "/x", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	if gap < 900*time.Millisecond {
		t.Errorf("retried after %s, want ≥1s", gap)
	}
}

func TestRetryAfterBeyondDeadlineFailsFast(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		writeJSON(w, 429, map[string]any{"message": "slow down", "code": "rate_limited"})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	err := c.Do(ctx, http.MethodGet, "/x", nil, nil)
	if apiErr, ok := AsAPIError(err); !ok || apiErr.Code != "rate_limited" {
		t.Errorf("err = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Error("waited although Retry-After exceeds the deadline")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		d  time.Duration
		ok bool
	}{
		"":                              {0, false},
		"7":                             {7 * time.Second, true},
		"-1":                            {0, false},
		"soon":                          {0, false},
		"Sun, 25 Oct 2026 12:00:30 GMT": {30 * time.Second, true},
		"Sun, 25 Oct 2026 11:00:00 GMT": {0, true},
	}
	for in, want := range cases {
		d, ok := retryAfter(in, now)
		if d != want.d || ok != want.ok {
			t.Errorf("retryAfter(%q) = %s, %v; want %s, %v", in, d, ok, want.d, want.ok)
		}
	}
}

func TestJitteredBackoffBounds(t *testing.T) {
	for n := 1; n <= 40; n++ {
		d := jitteredBackoff(n)
		ceiling := min(baseBackoff<<(n-1), maxBackoff)
		if n > 20 {
			ceiling = maxBackoff
		}
		if d < ceiling/2 || d > ceiling {
			t.Errorf("backoff(%d) = %s outside [%s, %s]", n, d, ceiling/2, ceiling)
		}
	}
}

func TestNetworkErrors(t *testing.T) {
	// A closed listener: every attempt is a dial error, safe to retry for
	// any method.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	c, _ := New(Config{BaseURL: "http://" + addr, Token: "t"})
	var attempts atomic.Int32
	c.backoff = func(int) time.Duration { attempts.Add(1); return time.Millisecond }
	if err := c.Do(context.Background(), http.MethodPost, "/x", map[string]any{}, nil); err == nil {
		t.Fatal("want error")
	}
	if attempts.Load() != 4 {
		t.Errorf("POST dial error: %d retries, want 4", attempts.Load())
	}

	// A connection dropped after the request was sent: GET retries, POST
	// must not (the server may have acted on it).
	for _, tc := range []struct {
		method string
		want   int32
	}{{http.MethodGet, 5}, {http.MethodPost, 1}} {
		var calls atomic.Int32
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("no hijacker")
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		})
		if err := c.Do(context.Background(), tc.method, "/x", map[string]any{}, nil); err == nil {
			t.Errorf("%s: want error", tc.method)
		}
		if calls.Load() != tc.want {
			t.Errorf("%s after a dropped connection: %d calls, want %d", tc.method, calls.Load(), tc.want)
		}
	}
}

func TestContextCancelled(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 503, map[string]any{"message": "busy", "code": "server_error"})
	})
	c.backoff = func(int) time.Duration { return time.Hour }
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	if err := c.Do(ctx, http.MethodGet, "/x", nil, nil); err == nil {
		t.Fatal("want error")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("cancel did not interrupt the wait")
	}
}

func TestDecodeError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not json")
	})
	var out thing
	if err := c.Do(context.Background(), http.MethodGet, "/x", nil, &out); err == nil || !strings.Contains(err.Error(), "decoding response") {
		t.Errorf("err = %v", err)
	}
}

func TestTokenNeverInErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 401, map[string]any{"message": "Unauthenticated.", "code": "unauthenticated"})
	})
	err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil)
	if strings.Contains(err.Error(), "rtm_secret") {
		t.Errorf("token leaked into %q", err)
	}
}
