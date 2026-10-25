package provider

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// Unit tests: no network beyond httptest, no TF_ACC.

func clearProviderEnv(t *testing.T) {
	t.Setenv(EnvToken, "")
	t.Setenv(EnvOrg, "")
	t.Setenv(EnvBaseURL, "")
}

func TestProviderMissingToken(t *testing.T) {
	clearProviderEnv(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      `data "routeur_me" "x" {}`,
			ExpectError: regexp.MustCompile(`Missing routeur.ai API token`),
		}},
	})
}

func TestProviderInvalidBaseURL(t *testing.T) {
	clearProviderEnv(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
provider "routeur" {
  token    = "rtm_x"
  base_url = "app.routeur.ai/api/v1"
}
data "routeur_me" "x" {}`,
			ExpectError: regexp.MustCompile(`Invalid routeur.ai base URL`),
		}},
	})
}

// With no org configured, the provider asks GET /me and uses its slug.
func TestProviderDefaultsOrgFromMe(t *testing.T) {
	clearProviderEnv(t)
	var mu sync.Mutex
	var userAgents []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		userAgents = append(userAgents, r.Header.Get("User-Agent"))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer rtm_unit" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"Unauthenticated.","code":"unauthenticated"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/me":
			_, _ = w.Write([]byte(`{"data":{"user":{"id":"user_1","email":"a@example.com","name":"A"},"organisation":{"slug":"acme","name":"Acme"},"token":{"name":"t","fingerprint":"rtm_…abcd","expires_at":null}}}`))
		case "/api/v1/orgs/acme/workspaces/default":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": apiWorkspace{ID: "ws_1", Slug: "default", Name: "Default", IsDefault: true, CreatedAt: "2026-10-25T00:00:00Z", UpdatedAt: "2026-10-25T00:00:00Z"}})
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Not found.","code":"not_found"}`))
		}
	}))
	defer srv.Close()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
provider "routeur" {
  token    = "rtm_unit"
  base_url = "` + srv.URL + `/api/v1"
}
data "routeur_workspace" "default" { slug = "default" }`,
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("data.routeur_workspace.default", "id", "ws_1"),
				resource.TestCheckResourceAttr("data.routeur_workspace.default", "is_default", "true"),
				resource.TestCheckNoResourceAttr("data.routeur_workspace.default", "description"),
			),
		}},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(userAgents) == 0 || !regexp.MustCompile(`^terraform-provider-routeur/test \(\+terraform \d+\.\d+`).MatchString(userAgents[0]) {
		t.Errorf("User-Agent = %q", userAgents)
	}
}

// fakeMeServer answers GET /me for token rtm_unit with organisation acme,
// a 401 for any other token, and counts every request by path.
func fakeMeServer(t *testing.T) (*httptest.Server, func() map[string]int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer rtm_unit" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"Unauthenticated.","code":"unauthenticated"}`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/me":
			_, _ = w.Write([]byte(`{"data":{"user":{"id":"user_1","email":"a@example.com","name":"A"},"organisation":{"slug":"acme","name":"Acme"},"token":{"name":"t","fingerprint":"rtm_…abcd","expires_at":null}}}`))
		case "/api/v1/orgs/acme/workspaces/default":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": apiWorkspace{ID: "ws_1", Slug: "default", Name: "Default", IsDefault: true, CreatedAt: "2026-10-25T00:00:00Z", UpdatedAt: "2026-10-25T00:00:00Z"}})
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Not found.","code":"not_found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int{}
		for k, v := range hits {
			out[k] = v
		}
		return out
	}
}

// A configured org that is not the token's own is a provider error, and no
// resource or data source call is made.
func TestProviderOrgMismatch(t *testing.T) {
	clearProviderEnv(t)
	srv, hits := fakeMeServer(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
provider "routeur" {
  token    = "rtm_unit"
  org      = "does-not-exist"
  base_url = "` + srv.URL + `/api/v1"
}
data "routeur_workspace" "default" { slug = "default" }`,
			ExpectError: regexp.MustCompile(`token belongs to organisation\s+"acme",\s+not\s+"does-not-exist"`),
		}},
	})
	for p := range hits() {
		if p != "/api/v1/me" {
			t.Errorf("unexpected request to %s after the org mismatch", p)
		}
	}
}

// The org can also come from ROUTEUR_ORG; it is checked the same way.
func TestProviderOrgMismatchFromEnv(t *testing.T) {
	clearProviderEnv(t)
	srv, _ := fakeMeServer(t)
	t.Setenv(EnvOrg, "other")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
provider "routeur" {
  token    = "rtm_unit"
  base_url = "` + srv.URL + `/api/v1"
}
data "routeur_workspace" "default" { slug = "default" }`,
			ExpectError: regexp.MustCompile(`token belongs to organisation\s+"acme",\s+not\s+"other"`),
		}},
	})
}

// A matching org works, and GET /me is still asked once per configure.
func TestProviderOrgMatches(t *testing.T) {
	clearProviderEnv(t)
	srv, hits := fakeMeServer(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
provider "routeur" {
  token    = "rtm_unit"
  org      = "acme"
  base_url = "` + srv.URL + `/api/v1"
}
data "routeur_workspace" "default" { slug = "default" }`,
			Check: resource.TestCheckResourceAttr("data.routeur_workspace.default", "id", "ws_1"),
		}},
	})
	if hits()["/api/v1/me"] == 0 {
		t.Error("GET /me was not called with org set")
	}
}

// A bad token is one provider-level error from GET /me, not one 401 per
// resource.
func TestProviderBadToken(t *testing.T) {
	clearProviderEnv(t)
	srv, hits := fakeMeServer(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
provider "routeur" {
  token    = "rtm_wrong"
  org      = "acme"
  base_url = "` + srv.URL + `/api/v1"
}
data "routeur_workspace" "a" { slug = "default" }
data "routeur_workspace" "b" { slug = "default" }`,
			ExpectError: regexp.MustCompile(`(?s)Could not authenticate with routeur.ai \(GET /me\).*code unauthenticated`),
		}},
	})
	for p := range hits() {
		if p != "/api/v1/me" {
			t.Errorf("unexpected request to %s with a bad token", p)
		}
	}
}

func TestSplitImportID(t *testing.T) {
	got, err := splitImportID("ws_3/proj_9", "workspace_id", "id")
	if err != nil || got[0] != "ws_3" || got[1] != "proj_9" {
		t.Errorf("got %v, %v", got, err)
	}
	// The last piece keeps its slashes (model slugs).
	got, err = splitImportID("ws_3/google:models/gemini-pro", "workspace_id", "model")
	if err != nil || got[1] != "google:models/gemini-pro" {
		t.Errorf("got %v, %v", got, err)
	}
	for _, bad := range []string{"ws_3", "ws_3/", "/proj_9", ""} {
		if _, err := splitImportID(bad, "workspace_id", "id"); err == nil {
			t.Errorf("splitImportID(%q): want error", bad)
		} else if !strings.Contains(err.Error(), "<workspace_id>/<id>") {
			t.Errorf("error does not show the expected form: %v", err)
		}
	}
}

func TestImportWorkspaceChildValidates(t *testing.T) {
	r := NewProjectResource()
	var sresp fwresource.SchemaResponse
	r.Schema(context.Background(), fwresource.SchemaRequest{}, &sresp)
	for id, want := range map[string]string{
		"default/proj_1": "workspace_id must be a workspace id",
		"ws_1/key_1":     "id must look like proj_12",
		"ws_1":           "expected an import id of the form",
	} {
		resp := &fwresource.ImportStateResponse{State: emptyState(sresp)}
		importWorkspaceChild(context.Background(), fwresource.ImportStateRequest{ID: id}, resp, "proj")
		if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), want) {
			t.Errorf("import %q: diagnostics %v, want %q", id, resp.Diagnostics, want)
		}
	}
}

func TestAttributePath(t *testing.T) {
	r := NewWorkspaceResource()
	var sresp fwresource.SchemaResponse
	r.Schema(context.Background(), fwresource.SchemaRequest{}, &sresp)
	ctx := context.Background()

	if p, ok := attributePath(ctx, sresp.Schema, "slug"); !ok || !p.Equal(path.Root("slug")) {
		t.Errorf("slug -> %v, %v", p, ok)
	}
	if _, ok := attributePath(ctx, sresp.Schema, "nope"); ok {
		t.Error("unknown field mapped")
	}
	if _, ok := attributePath(ctx, sresp.Schema, "name.inner"); ok {
		t.Error("nested path into a string mapped")
	}
	if p, ok := attributePath(ctx, sresp.Schema, "label", map[string]string{"label": "name"}); !ok || !p.Equal(path.Root("name")) {
		t.Errorf("alias -> %v, %v", p, ok)
	}
	if _, ok := attributePath(ctx, nil, "slug"); ok {
		t.Error("nil schema mapped")
	}
}

func TestAddAPIError(t *testing.T) {
	r := NewWorkspaceResource()
	var sresp fwresource.SchemaResponse
	r.Schema(context.Background(), fwresource.SchemaRequest{}, &sresp)
	ctx := context.Background()

	t.Run("field errors attach to attributes", func(t *testing.T) {
		var diags diag.Diagnostics
		addAPIError(ctx, &diags, sresp.Schema, "Could not create workspace", &client.APIError{
			Status: 422, Code: "validation_failed", Message: "The given data was invalid.",
			Fields: map[string][]string{"slug": {"The slug has already been taken."}},
		})
		if len(diags) != 1 {
			t.Fatalf("diags = %v", diags)
		}
		d, ok := diags[0].(diag.DiagnosticWithPath)
		if !ok || !d.Path().Equal(path.Root("slug")) || !strings.Contains(d.Detail(), "already been taken") || !strings.Contains(d.Detail(), "code validation_failed") {
			t.Errorf("diag = %#v", diags[0])
		}
	})

	t.Run("unmapped fields stay in a general error", func(t *testing.T) {
		var diags diag.Diagnostics
		addAPIError(ctx, &diags, sresp.Schema, "Could not create workspace", &client.APIError{
			Status: 422, Code: "validation_failed", Message: "The given data was invalid.",
			Fields: map[string][]string{"slug": {"Taken."}, "mystery": {"Odd."}},
		})
		if len(diags) != 2 || !strings.Contains(diags[1].Detail(), "mystery: Odd.") {
			t.Errorf("diags = %v", diags)
		}
	})

	t.Run("domain refusal carries code and hint", func(t *testing.T) {
		var diags diag.Diagnostics
		addAPIError(ctx, &diags, sresp.Schema, "Could not delete workspace", &client.APIError{
			Status: 422, Code: "workspace_not_empty", Message: "Delete this workspace’s projects before deleting it.", RequestID: "tf-1",
		})
		detail := diags[0].Detail()
		for _, want := range []string{"Delete this workspace’s projects", "referencing the workspace's id", "HTTP 422, code workspace_not_empty, request id tf-1"} {
			if !strings.Contains(detail, want) {
				t.Errorf("detail lacks %q:\n%s", want, detail)
			}
		}
	})

	t.Run("feature unavailable names the plan", func(t *testing.T) {
		var diags diag.Diagnostics
		addAPIError(ctx, &diags, nil, "Could not create budget", &client.APIError{
			Status: 403, Code: "feature_unavailable", Message: "Your plan does not include budgets.", Feature: "budgets", RequiredPlan: "pro",
		})
		if !strings.Contains(diags[0].Detail(), `The "budgets" feature needs the "pro" plan or above.`) {
			t.Errorf("detail = %s", diags[0].Detail())
		}
	})

	t.Run("non-API errors pass through", func(t *testing.T) {
		var diags diag.Diagnostics
		addAPIError(ctx, &diags, nil, "Could not read", errors.New("dial tcp: refused"))
		if diags[0].Detail() != "dial tcp: refused" {
			t.Errorf("detail = %s", diags[0].Detail())
		}
	})
}

func TestNoSurroundingWhitespace(t *testing.T) {
	v := noSurroundingWhitespace()
	for in, wantErr := range map[string]bool{"ok": false, "two words": false, " lead": true, "trail ": true, "": true, "   ": true, "\ttab": true} {
		resp := &validator.StringResponse{}
		v.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("name"), ConfigValue: types.StringValue(in)}, resp)
		if resp.Diagnostics.HasError() != wantErr {
			t.Errorf("%q: error = %v, want %v", in, resp.Diagnostics.HasError(), wantErr)
		}
	}
	resp := &validator.StringResponse{}
	v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringNull()}, resp)
	if resp.Diagnostics.HasError() {
		t.Error("null value refused")
	}
}

func TestSlugPatterns(t *testing.T) {
	for s, want := range map[string]bool{"a": true, "a-b": true, "a1-2b": true, "-a": false, "a-": false, "A": false, "a_b": false} {
		if hyphenSlugPattern.MatchString(s) != want {
			t.Errorf("hyphen slug %q: want %v", s, want)
		}
	}
	for s, want := range map[string]bool{"a": true, "a_b": true, "a__b": false, "_a": false, "a_": false, "a-b": false} {
		if underscoreSlugPattern.MatchString(s) != want {
			t.Errorf("underscore slug %q: want %v", s, want)
		}
	}
	if !publicIDPattern("ws").MatchString("ws_12") || publicIDPattern("ws").MatchString("ws_0") || publicIDPattern("ws").MatchString("proj_1") {
		t.Error("publicIDPattern")
	}
}

func emptyState(s fwresource.SchemaResponse) tfsdk.State {
	return tfsdk.State{Schema: s.Schema, Raw: tftypes.NewValue(s.Schema.Type().TerraformType(context.Background()), nil)}
}

// runStringValidators runs vs on v and returns the diagnostics.
func runStringValidators(vs []validator.String, v types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, val := range vs {
		resp := &validator.StringResponse{}
		val.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("name"), ConfigValue: v}, resp)
		diags.Append(resp.Diagnostics...)
	}
	return diags
}

// Lengths count characters (UTF-8 code points), as the API does, not
// bytes: 80 two-byte characters are a valid 80-character name.
func TestNameLengthCountsCharacters(t *testing.T) {
	for in, wantErrs := range map[string]int{
		strings.Repeat("é", 80): 0,
		strings.Repeat("é", 81): 1,
		strings.Repeat("🙂", 80): 0,
		strings.Repeat("a", 81): 1,
		"":                      1, // one error, not "too short" and "blank"
		"  ":                    1,
	} {
		if got := runStringValidators(name(80), types.StringValue(in)).ErrorsCount(); got != wantErrs {
			t.Errorf("name(80) on %q (%d bytes): %d errors, want %d", in, len(in), got, wantErrs)
		}
	}
	// Slugs: "" is one error too.
	if got := runStringValidators(hyphenSlug(projectSlugMax), types.StringValue("")).ErrorsCount(); got != 1 {
		t.Errorf("hyphenSlug on \"\": %d errors, want 1", got)
	}
	// A workspace slug leaves room for its default project's "-core" suffix.
	for n, want := range map[int]int{workspaceSlugMax: 0, workspaceSlugMax + 1: 1} {
		if got := runStringValidators(hyphenSlug(workspaceSlugMax), types.StringValue(strings.Repeat("a", n))).ErrorsCount(); got != want {
			t.Errorf("hyphenSlug(%d) on %d characters: %d errors, want %d", workspaceSlugMax, n, got, want)
		}
	}
	if got := runStringValidators(underscoreSlug(), types.StringValue("")).ErrorsCount(); got != 1 {
		t.Errorf("underscoreSlug on \"\": %d errors, want 1", got)
	}
	// Unknown values (from another resource) pass every validator.
	if got := runStringValidators(name(80), types.StringUnknown()).ErrorsCount(); got != 0 {
		t.Errorf("unknown: %d errors", got)
	}
}

// Org-level imports check the id's shape too: budgets take bud_N,
// workspaces ws_N or a slug.
func TestImportIDValidation(t *testing.T) {
	cases := []struct {
		r       fwresource.Resource
		id      string
		wantErr bool
	}{
		{NewBudgetResource(), "bud_3", false},
		{NewBudgetResource(), "3", true},
		{NewBudgetResource(), "ws_3", true},
		{NewWorkspaceResource(), "ws_3", false},
		{NewWorkspaceResource(), "production", false},
		{NewWorkspaceResource(), "Production", true},
		{NewWorkspaceResource(), "ws_3/proj_1", true},
		{NewWorkspaceResource(), "", true},
	}
	for _, c := range cases {
		var sresp fwresource.SchemaResponse
		c.r.Schema(context.Background(), fwresource.SchemaRequest{}, &sresp)
		resp := &fwresource.ImportStateResponse{State: emptyState(sresp)}
		importer, ok := c.r.(fwresource.ResourceWithImportState)
		if !ok {
			t.Fatalf("%T is not importable", c.r)
		}
		importer.ImportState(context.Background(), fwresource.ImportStateRequest{ID: c.id}, resp)
		if resp.Diagnostics.HasError() != c.wantErr {
			t.Errorf("%T import %q: diagnostics %v, want error %v", c.r, c.id, resp.Diagnostics, c.wantErr)
		}
	}
}

func TestDuplicateCredentialHint(t *testing.T) {
	r := NewProviderCredentialResource()
	var sresp fwresource.SchemaResponse
	r.Schema(context.Background(), fwresource.SchemaRequest{}, &sresp)
	dup := &client.APIError{Status: 422, Code: "validation_failed", Message: "The given data was invalid.",
		Fields: map[string][]string{"provider": {"This organisation already has a credential for Google. Rotate its key instead."}}}
	var diags diag.Diagnostics
	addAPIError(context.Background(), &diags, sresp.Schema, "Could not create google credential", withDuplicateCredentialHint(dup), providerNameAlias)
	if len(diags) != 1 || !strings.Contains(diags[0].Detail(), "terraform import routeur_provider_credential.<name> cred_<N>") {
		t.Errorf("diags = %v", diags)
	}
	if len(dup.Fields["provider"]) != 1 {
		t.Error("the original error was modified")
	}
	other := &client.APIError{Status: 422, Fields: map[string][]string{"label": {"Taken."}}}
	if withDuplicateCredentialHint(other) != error(other) {
		t.Error("an unrelated error was changed")
	}
}

func TestGrowingEnumWarns(t *testing.T) {
	v := growingEnum("provider", "openai", "google")
	for in, wantWarn := range map[string]bool{"openai": false, "azure": true} {
		resp := &validator.StringResponse{}
		v.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("provider_name"), ConfigValue: types.StringValue(in)}, resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("%q: error %v; an unknown provider is only a warning", in, resp.Diagnostics)
		}
		if (resp.Diagnostics.WarningsCount() == 1) != wantWarn {
			t.Errorf("%q: warnings %v, want warning %v", in, resp.Diagnostics, wantWarn)
		}
	}
}

// decimalFloat64 must equal what Terraform sends for the same number written
// in configuration; types.Float64Value(f) does not for 0.755.
func TestDecimalFloat64MatchesConfiguration(t *testing.T) {
	for _, s := range []string{"0.755", "2.755", "0.1", "1", "0", "100", "0.01"} {
		cfg, _, err := big.ParseFloat(s, 10, 512, big.ToNearestEven)
		if err != nil {
			t.Fatal(err)
		}
		fromConfig := tftypes.NewValue(tftypes.Number, cfg)
		got, err := decimalFloat64(json.Number(s)).ToTerraformValue(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(fromConfig) {
			t.Errorf("%s: decimalFloat64 = %s, configuration = %s", s, got, fromConfig)
		}
	}
	naive, _ := types.Float64Value(0.755).ToTerraformValue(context.Background())
	cfg, _, _ := big.ParseFloat("0.755", 10, 512, big.ToNearestEven)
	if naive.Equal(tftypes.NewValue(tftypes.Number, cfg)) {
		t.Log("types.Float64Value(0.755) now equals the configured 0.755; decimalFloat64 may no longer be needed")
	}
	if !decimalFloat64Pointer(nil).IsNull() {
		t.Error("nil is not null")
	}
}
