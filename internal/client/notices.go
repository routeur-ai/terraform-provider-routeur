package client

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Notice is a deprecation signal on an API response: the Deprecation
// (RFC 9745) and/or Sunset (RFC 8594) header. The API sends them on
// deprecated fields and endpoints, which keep working until the sunset.
type Notice struct {
	// Method and Path are the first request that carried this notice.
	Method, Path string
	// Deprecation is when the resource was (or will be) deprecated, as
	// sent: RFC 9745 uses `@<unix seconds>`; Since renders it.
	Deprecation string
	// Sunset is the HTTP-date after which it may stop working.
	Sunset string
	// Links are the response's Link header values that point at
	// documentation (rel="deprecation" or rel="sunset").
	Links []string
}

// Since renders Deprecation as a UTC date, or returns it as sent when it is
// not an RFC 9745 date (`@<unix seconds>`).
func (n Notice) Since() string {
	if unix, err := strconv.ParseInt(strings.TrimPrefix(n.Deprecation, "@"), 10, 64); err == nil && strings.HasPrefix(n.Deprecation, "@") {
		return time.Unix(unix, 0).UTC().Format(time.RFC3339)
	}
	return n.Deprecation
}

// Notices collects the distinct notices seen during one operation. It is
// safe for concurrent use.
type Notices struct {
	mu   sync.Mutex
	list []Notice
	seen map[string]bool
}

type noticesKey struct{}

// WithNotices returns a context whose requests record their Deprecation and
// Sunset headers into the returned Notices. Use one per Terraform operation
// (a resource's Create, Read, …) so each notice is reported once for it.
func WithNotices(ctx context.Context) (context.Context, *Notices) {
	n := &Notices{seen: map[string]bool{}}
	return context.WithValue(ctx, noticesKey{}, n), n
}

// List returns the notices in the order they were first seen.
func (n *Notices) List() []Notice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Notice(nil), n.list...)
}

// recordNotice adds h's deprecation headers, if any, to ctx's Notices.
// The same headers on several requests (pagination, a read after a write)
// are one notice.
func recordNotice(ctx context.Context, method, path string, h http.Header) {
	dep, sunset := strings.TrimSpace(h.Get("Deprecation")), strings.TrimSpace(h.Get("Sunset"))
	if dep == "" && sunset == "" {
		return
	}
	n, ok := ctx.Value(noticesKey{}).(*Notices)
	if !ok {
		return
	}
	var links []string
	for _, l := range h.Values("Link") {
		for _, part := range strings.Split(l, ",") {
			lower := strings.ToLower(part)
			if strings.Contains(lower, `rel="deprecation"`) || strings.Contains(lower, `rel="sunset"`) ||
				strings.Contains(lower, "rel=deprecation") || strings.Contains(lower, "rel=sunset") {
				links = append(links, strings.TrimSpace(part))
			}
		}
	}
	key := dep + "\x00" + sunset + "\x00" + strings.Join(links, "\x00")
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seen[key] {
		return
	}
	n.seen[key] = true
	n.list = append(n.list, Notice{Method: method, Path: stripQuery(path), Deprecation: dep, Sunset: sunset, Links: links})
}

func stripQuery(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}
