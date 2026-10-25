package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// PageSize is the limit ListAll asks for: the API maximum.
const PageSize = 200

// maxPages stops a misbehaving server from looping a refresh forever.
const maxPages = 10_000

// Single is the `{"data": …}` wrapper of every single-resource response.
type Single[T any] struct {
	Data T `json:"data"`
}

// page is one cursor-paginated list response: `{data, links, meta}`. Only
// meta.next_cursor is followed: links.next is an absolute URL that may not
// match BaseURL behind a proxy.
type page[T any] struct {
	Data []T `json:"data"`
	Meta struct {
		NextCursor *string `json:"next_cursor"`
	} `json:"meta"`
}

// Get fetches path and unwraps its `data`.
func Get[T any](ctx context.Context, c *Client, path string) (T, error) {
	var out Single[T]
	err := c.Do(ctx, http.MethodGet, path, nil, &out)
	return out.Data, err
}

// Send sends body with method to path and unwraps the `data` of the response.
func Send[T any](ctx context.Context, c *Client, method, path string, body any) (T, error) {
	var out Single[T]
	err := c.Do(ctx, method, path, body, &out)
	return out.Data, err
}

// ListAll fetches every page of a collection, oldest first.
func ListAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	cursor := ""
	seen := map[string]bool{}
	for range maxPages {
		q := url.Values{"limit": {fmt.Sprint(PageSize)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		var p page[T]
		if err := c.Do(ctx, http.MethodGet, path+sep+q.Encode(), nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Data...)
		if p.Meta.NextCursor == nil || *p.Meta.NextCursor == "" {
			return all, nil
		}
		cursor = *p.Meta.NextCursor
		if seen[cursor] {
			return nil, fmt.Errorf("GET %s: the API returned cursor %q twice", path, cursor)
		}
		seen[cursor] = true
	}
	return nil, fmt.Errorf("GET %s: more than %d pages", path, maxPages)
}
