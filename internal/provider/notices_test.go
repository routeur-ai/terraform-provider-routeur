package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// fakeAPIResource calls the API twice per Read, so the test can check a
// notice is reported once per operation.
type fakeAPIResource struct {
	resource.Resource // nil: only Read is called
	c                 *client.Client
}

func (f fakeAPIResource) Read(ctx context.Context, _ resource.ReadRequest, _ *resource.ReadResponse) {
	_ = f.c.Do(ctx, http.MethodGet, "/old?cursor=1", nil, nil)
	_ = f.c.Do(ctx, http.MethodGet, "/old?cursor=2", nil, nil)
}

func TestDeprecationHeadersBecomeOneWarningPerOperation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Deprecation", "@1767225600")
		w.Header().Set("Sunset", "Sat, 01 Aug 2026 00:00:00 GMT")
		w.Header().Set("Link", `<https://docs.routeur.ai/deprecations>; rel="deprecation"`)
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()
	c, err := client.New(client.Config{BaseURL: srv.URL, Token: "rtm_x"})
	if err != nil {
		t.Fatal(err)
	}

	r := withNotices(fakeAPIResource{c: c})
	for range 2 { // each operation reports its own notices
		resp := &resource.ReadResponse{}
		r.Read(context.Background(), resource.ReadRequest{}, resp)
		if resp.Diagnostics.WarningsCount() != 1 || resp.Diagnostics.HasError() {
			t.Fatalf("diagnostics = %v, want exactly one warning", resp.Diagnostics)
		}
		detail := resp.Diagnostics.Warnings()[0].Detail()
		for _, want := range []string{"GET /old uses a deprecated", "deprecated since 2026-01-01T00:00:00Z", "after Sat, 01 Aug 2026 00:00:00 GMT", "https://docs.routeur.ai/deprecations"} {
			if !strings.Contains(detail, want) {
				t.Errorf("warning lacks %q:\n%s", want, detail)
			}
		}
	}
}

// The wrapper hides the wrapped resource's type from the framework, so it
// must forward every optional interface a resource implements. These are
// the framework interfaces it does not forward: a resource that gains one
// must be added to noticeResource first.
func TestResourceWrapperForwardsInterfaces(t *testing.T) {
	for _, f := range resourceFactories {
		w, ok := f().(noticeResource)
		if !ok {
			t.Fatalf("%T is not wrapped by registerResource", f())
		}
		name := w.Resource
		if _, ok := w.Resource.(resource.ResourceWithImportState); !ok {
			t.Errorf("%T does not implement ImportState (every resource is importable)", name)
		}
		for iface, has := range map[string]bool{
			"UpgradeState":    implements[resource.ResourceWithUpgradeState](w.Resource),
			"MoveState":       implements[resource.ResourceWithMoveState](w.Resource),
			"Identity":        implements[resource.ResourceWithIdentity](w.Resource),
			"UpgradeIdentity": implements[resource.ResourceWithUpgradeIdentity](w.Resource),
		} {
			if has {
				t.Errorf("%T implements %s, which noticeResource does not forward", name, iface)
			}
		}
	}
	for _, f := range dataSourceFactories {
		if _, ok := f().(noticeDataSource); !ok {
			t.Fatalf("%T is not wrapped by registerDataSource", f())
		}
	}
}

func implements[I any](v any) bool {
	_, ok := v.(I)
	return ok
}
