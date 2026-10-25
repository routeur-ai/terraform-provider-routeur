package client

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// listHandler serves `total` things in pages of `per`, with the real
// envelope shape: {data, links, meta}.
func listHandler(t *testing.T, total int, calls *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*calls++
		q := r.URL.Query()
		if q.Get("limit") != "200" {
			t.Errorf("limit = %q, want 200", q.Get("limit"))
		}
		per := 3
		start := 0
		if c := q.Get("cursor"); c != "" {
			start, _ = strconv.Atoi(strings.TrimPrefix(c, "c"))
		}
		var data []thing
		for i := start; i < start+per && i < total; i++ {
			data = append(data, thing{ID: "ws_" + strconv.Itoa(i+1)})
		}
		var next any
		if start+per < total {
			next = "c" + strconv.Itoa(start+per)
		}
		writeJSON(w, 200, map[string]any{
			"data":  data,
			"links": map[string]any{"first": nil, "last": nil, "prev": nil, "next": "https://elsewhere.example/ignored"},
			"meta":  map[string]any{"path": "https://elsewhere.example", "per_page": per, "next_cursor": next, "prev_cursor": nil},
		})
	}
}

func TestListAll(t *testing.T) {
	for _, total := range []int{0, 1, 3, 7} {
		calls := 0
		c := newTestClient(t, listHandler(t, total, &calls))
		got, err := ListAll[thing](context.Background(), c, "/orgs/acme/workspaces")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != total {
			t.Errorf("total %d: got %d items", total, len(got))
		}
		for i, th := range got {
			if th.ID != "ws_"+strconv.Itoa(i+1) {
				t.Errorf("item %d = %s (order lost)", i, th.ID)
			}
		}
		wantCalls := max(1, (total+2)/3)
		if calls != wantCalls {
			t.Errorf("total %d: %d calls, want %d", total, calls, wantCalls)
		}
	}
}

func TestListAllExistingQuery(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("active") != "true" || r.URL.Query().Get("limit") != "200" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		writeJSON(w, 200, map[string]any{"data": []thing{}, "meta": map[string]any{"next_cursor": nil}})
	})
	if _, err := ListAll[thing](context.Background(), c, "/x?active=true"); err != nil {
		t.Fatal(err)
	}
}

func TestListAllRepeatedCursor(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"data": []thing{{ID: "a"}}, "meta": map[string]any{"next_cursor": "same"}})
	})
	if _, err := ListAll[thing](context.Background(), c, "/x"); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("err = %v", err)
	}
}

func TestListAllError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]any{"message": "Not found.", "code": "not_found"})
	})
	if _, err := ListAll[thing](context.Background(), c, "/x"); !IsNotFound(err) {
		t.Errorf("err = %v", err)
	}
}
