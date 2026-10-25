// Package client is a small hand-written client for the routeur.ai
// management API (https://app.routeur.ai/api/v1).
package client

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// DefaultBaseURL is the production management API.
const DefaultBaseURL = "https://app.routeur.ai/api/v1"

const (
	// defaultMaxAttempts caps attempts at a request that fails with a
	// 502/503/504 or a network error.
	defaultMaxAttempts = 5
	// defaultRateLimitBudget bounds the total time spent retrying 429s. The
	// API's limits are per minute, so a large apply waits them out.
	defaultRateLimitBudget = 5 * time.Minute
	baseBackoff            = 500 * time.Millisecond
	maxBackoff             = 30 * time.Second
	maxErrorBody           = 64 << 10
)

// Config configures a Client.
type Config struct {
	// BaseURL defaults to DefaultBaseURL.
	BaseURL string
	// Token is the API token (rtm_…). Required.
	Token string
	// UserAgent is sent on every request.
	UserAgent string
	// HTTPClient defaults to a client with a 60s timeout.
	HTTPClient *http.Client
}

// Client talks to the management API. It is safe for concurrent use.
type Client struct {
	baseURL     string
	token       string
	userAgent   string
	httpClient  *http.Client
	maxAttempts int
	// rateLimitBudget is how long, from the first attempt, 429s are
	// retried (also bounded by the context deadline).
	rateLimitBudget time.Duration
	// backoff returns the wait before retry number n (1-based) when the
	// server gave no Retry-After. Replaced in tests.
	backoff func(n int) time.Duration
}

// New validates cfg and returns a Client.
func New(cfg Config) (*Client, error) {
	if cfg.Token == "" {
		return nil, errors.New("an API token is required")
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("base URL %q must be an absolute http(s) URL", base)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{
		baseURL:         strings.TrimRight(base, "/"),
		token:           cfg.Token,
		userAgent:       cfg.UserAgent,
		httpClient:      hc,
		maxAttempts:     defaultMaxAttempts,
		rateLimitBudget: defaultRateLimitBudget,
		backoff:         jitteredBackoff,
	}, nil
}

// Path joins segments into an API path, escaping each one with
// url.PathEscape: model slugs such as `google:models/gemini-pro` contain `/`
// that belongs to the value, not the path.
//
//	client.Path("orgs", org, "workspaces", ws) // "/orgs/acme/workspaces/ws_3"
func Path(segments ...string) string {
	var b strings.Builder
	for _, s := range segments {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(s))
	}
	return b.String()
}

// Do sends method to path (as built by Path, optionally with a query
// string), JSON-encoding in when it is non-nil and decoding a 2xx body into
// out when it is non-nil. A non-2xx response returns an *APIError.
//
// Retries: 429 always, honouring Retry-After, for up to 5 minutes from the
// first attempt; 502/503/504 and network errors for idempotent methods
// (GET, PUT, DELETE) only, at most 5 attempts. POST and PATCH are never
// re-sent once the request may have reached the server. Every wait is
// bounded by ctx.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
	}
	requestID := newRequestID()
	start := time.Now()
	var serverRetries, rateLimitRetries int

	for attempt := 1; ; attempt++ {
		resp, err := c.send(ctx, method, path, body, requestID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logRequest(ctx, method, path, 0, requestID, attempt)
			if serverRetries+1 < c.maxAttempts && (idempotent(method) || isDialError(err)) {
				serverRetries++
				if werr := wait(ctx, c.backoff(serverRetries)); werr != nil {
					return fmt.Errorf("%s %s: %w (gave up waiting to retry: %v)", method, path, err, werr)
				}
				continue
			}
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		logRequest(ctx, method, path, resp.StatusCode, requestID, attempt)
		recordNotice(ctx, method, path, resp.Header)

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer func() { _ = resp.Body.Close() }()
			if out == nil || resp.StatusCode == http.StatusNoContent {
				_, _ = io.Copy(io.Discard, resp.Body)
				return nil
			}
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
			}
			return nil
		}

		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
		apiErr := newAPIError(resp.StatusCode, errBody, requestID)

		if !retryable(method, resp.StatusCode) {
			return apiErr
		}
		delay, hasRetryAfter := retryAfter(resp.Header.Get("Retry-After"), time.Now())
		if resp.StatusCode == http.StatusTooManyRequests {
			rateLimitRetries++
			if !hasRetryAfter {
				delay = c.backoff(rateLimitRetries)
			}
			if time.Since(start)+delay > c.rateLimitBudget {
				return apiErr
			}
		} else {
			if serverRetries+1 >= c.maxAttempts {
				return apiErr
			}
			serverRetries++
			if !hasRetryAfter {
				delay = c.backoff(serverRetries)
			}
		}
		if deadline, has := ctx.Deadline(); has && time.Until(deadline) < delay {
			return apiErr
		}
		tflog.Debug(ctx, "routeur.ai API: retrying", map[string]any{"status": resp.StatusCode, "delay": delay.String()})
		if err := wait(ctx, delay); err != nil {
			return apiErr
		}
	}
}

func (c *Client) send(ctx context.Context, method, path string, body []byte, requestID string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	// url.Parse keeps RawPath, so escaped segments (%2F) go out as escaped.
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Request-Id", requestID)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

// logRequest writes one line per HTTP attempt: never headers or bodies, so
// tokens and credential keys cannot leak into TF_LOG output.
func logRequest(ctx context.Context, method, path string, status int, requestID string, attempt int) {
	tflog.Debug(ctx, "routeur.ai API request", map[string]any{
		"method":     method,
		"path":       path,
		"status":     status,
		"request_id": requestID,
		"attempt":    attempt,
	})
}

func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

func retryable(method string, status int) bool {
	switch status {
	case http.StatusTooManyRequests:
		// A rate-limited request was not processed, so even a POST is safe
		// to re-send.
		return true
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return idempotent(method)
	}
	return false
}

// isDialError reports a failure to connect at all: the request never left,
// so any method may be retried.
func isDialError(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// retryAfter parses a Retry-After header: delay-seconds or an HTTP-date.
func retryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// jitteredBackoff is exponential backoff with full jitter.
func jitteredBackoff(n int) time.Duration {
	ceiling := baseBackoff << (n - 1)
	if ceiling > maxBackoff || ceiling <= 0 {
		ceiling = maxBackoff
	}
	return ceiling/2 + rand.N(ceiling/2+1)
}

func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func newRequestID() string {
	b := make([]byte, 12)
	_, _ = crand.Read(b)
	return "tf-" + hex.EncodeToString(b)
}
