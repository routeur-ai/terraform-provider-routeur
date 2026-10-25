package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// testAccAPIKeyConfig is a workspace with two projects and one key; body is
// the key's arguments besides workspace_id.
func testAccAPIKeyConfig(wsSlug, body string) string {
	return fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug = %[1]q
  name = "API key tests"
}

resource "routeur_project" "a" {
  workspace_id = routeur_workspace.test.id
  slug         = "%[1]s-a"
  name         = "A"
}

resource "routeur_project" "b" {
  workspace_id = routeur_workspace.test.id
  slug         = "%[1]s-b"
  name         = "B"
}

resource "routeur_api_key" "test" {
  workspace_id = routeur_workspace.test.id
  %[2]s
}
`, wsSlug, body)
}

func testAccCheckAPIKeyGone(t *testing.T) resource.TestCheckFunc {
	return testAccCheckGone(t, "routeur_api_key", func(org string, a map[string]string) string {
		return client.Path("orgs", org, "workspaces", a["workspace_id"], "api-keys", a["id"])
	})
}

var (
	testAccKeyIDPattern     = regexp.MustCompile(`^key_[1-9][0-9]*$`)
	testAccKeySecretPattern = regexp.MustCompile(`^sk-r-.{20,}$`)
)

func TestAccAPIKey_basic(t *testing.T) {
	ws := testAccSlug("ws")
	const res = "routeur_api_key.test"
	sameID := statecheck.CompareValue(compare.ValuesSame())
	sameSecret := statecheck.CompareValue(compare.ValuesSame())
	newID := statecheck.CompareValue(compare.ValuesDiffer())
	newSecret := statecheck.CompareValue(compare.ValuesDiffer())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckAPIKeyGone(t),
			testAccCheckProjectGone(t),
			testAccCheckWorkspaceGone(t),
		),
		Steps: []resource.TestStep{
			// Create in project a: the secret is returned once and kept.
			{
				Config: testAccAPIKeyConfig(ws, `
  name       = "Checkout"
  project_id = routeur_project.a.id
`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(testAccKeyIDPattern)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("secret"), knownvalue.StringRegexp(testAccKeySecretPattern)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("fingerprint"), knownvalue.StringRegexp(regexp.MustCompile(`^sk-r-…`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("state"), knownvalue.StringExact("active")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("disabled_at"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("revoked_at"), knownvalue.Null()),
					sameID.AddStateValue(res, tfjsonpath.New("id")),
					sameSecret.AddStateValue(res, tfjsonpath.New("secret")),
				},
				Check: resource.TestCheckResourceAttrPair(res, "project_id", "routeur_project.a", "id"),
			},
			// The secret cannot be read back, so import leaves it null.
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res), ImportStateVerifyIgnore: []string{"secret"}},
			// In place: rename, move to project b and disable. Same key, same secret.
			{
				Config: testAccAPIKeyConfig(ws, `
  name       = "Checkout v2"
  project_id = routeur_project.b.id
  enabled    = false
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("name"), knownvalue.StringExact("Checkout v2")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("state"), knownvalue.StringExact("disabled")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("disabled_at"), knownvalue.NotNull()),
					sameID.AddStateValue(res, tfjsonpath.New("id")),
					sameSecret.AddStateValue(res, tfjsonpath.New("secret")),
				},
				Check: resource.TestCheckResourceAttrPair(res, "project_id", "routeur_project.b", "id"),
			},
			// Removing `enabled` enables the key again.
			{
				Config: testAccAPIKeyConfig(ws, `
  name       = "Checkout v2"
  project_id = routeur_project.b.id
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("state"), knownvalue.StringExact("active")),
					sameID.AddStateValue(res, tfjsonpath.New("id")),
					sameSecret.AddStateValue(res, tfjsonpath.New("secret")),
					newID.AddStateValue(res, tfjsonpath.New("id")),
					newSecret.AddStateValue(res, tfjsonpath.New("secret")),
				},
			},
			// Rename and move without touching enabled: state, disabled_at and
			// revoked_at stay known in the plan (no "known after apply" noise).
			{
				Config: testAccAPIKeyConfig(ws, `
  name       = "Checkout v3"
  project_id = routeur_project.a.id
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("state"), knownvalue.StringExact("active")),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("disabled_at"), knownvalue.Null()),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("revoked_at"), knownvalue.Null()),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(res, tfjsonpath.New("id")),
					sameSecret.AddStateValue(res, tfjsonpath.New("secret")),
				},
				Check: resource.TestCheckResourceAttrPair(res, "project_id", "routeur_project.a", "id"),
			},
			// rotation_triggers first set on a key that had none: stored in
			// place, the key is unchanged.
			{
				Config: testAccAPIKeyConfig(ws, `
  name              = "Checkout v3"
  project_id        = routeur_project.a.id
  rotation_triggers = { rotated = "2026-09" }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(res, tfjsonpath.New("id")),
					sameSecret.AddStateValue(res, tfjsonpath.New("secret")),
				},
			},
			// rotation_triggers: a change replaces the key (new id, new secret).
			{
				Config: testAccAPIKeyConfig(ws, `
  name              = "Checkout v3"
  project_id        = routeur_project.a.id
  rotation_triggers = { rotated = "2026-10" }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("secret"), knownvalue.StringRegexp(testAccKeySecretPattern)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("rotation_triggers"), knownvalue.MapExact(map[string]knownvalue.Check{"rotated": knownvalue.StringExact("2026-10")})),
					newID.AddStateValue(res, tfjsonpath.New("id")),
					newSecret.AddStateValue(res, tfjsonpath.New("secret")),
				},
			},
			{
				Config: testAccAPIKeyConfig(ws, `
  name              = "Checkout v3"
  project_id        = routeur_project.a.id
  rotation_triggers = { rotated = "2026-10" }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// Adopting a live key: `terraform import` with rotation_triggers already in
// the configuration must not replace (and so revoke) the key being imported.
func TestAccAPIKey_importWithRotationTriggers(t *testing.T) {
	testAccSkipUnlessAcc(t)
	const res = "routeur_api_key.adopted"
	ctx := context.Background()
	c, org := testAccClient(t)

	ws, err := client.Send[apiWorkspace](ctx, c, http.MethodPost, client.Path("orgs", org, "workspaces"), map[string]any{"slug": testAccSlug("ws"), "name": "Adopted key"})
	if err != nil {
		t.Fatalf("creating workspace: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Do(ctx, http.MethodDelete, client.Path("orgs", org, "workspaces", ws.ID), nil, nil)
	})
	key, err := client.Send[apiAPIKey](ctx, c, http.MethodPost, client.Path("orgs", org, "workspaces", ws.ID, "api-keys"), map[string]any{"name": "Live"})
	if err != nil {
		t.Fatalf("creating API key: %v", err)
	}

	config := fmt.Sprintf(`
resource "routeur_api_key" "adopted" {
  workspace_id      = %q
  name              = "Live"
  rotation_triggers = { rotated = "2026-10" }
}
`, ws.ID)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAPIKeyGone(t),
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       res,
				ImportState:        true,
				ImportStateId:      ws.ID + "/" + key.ID,
				ImportStatePersist: true,
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringExact(key.ID)),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("state"), knownvalue.StringExact("active")),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringExact(key.ID)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("rotation_triggers"), knownvalue.MapExact(map[string]knownvalue.Check{"rotated": knownvalue.StringExact("2026-10")})),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("secret"), knownvalue.Null()),
				},
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// No project_id: the key goes to the workspace's default project. Created
// disabled: the API only creates active keys, so it is disabled after.
func TestAccAPIKey_defaultProjectDisabled(t *testing.T) {
	ws := testAccSlug("ws")
	const res = "routeur_api_key.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAPIKeyGone(t),
		Steps: []resource.TestStep{
			{
				Config: testAccAPIKeyConfig(ws, `
  name    = "Dormant"
  enabled = false
`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("state"), knownvalue.StringExact("disabled")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("secret"), knownvalue.StringRegexp(testAccKeySecretPattern)),
				},
				Check: resource.TestCheckResourceAttrPair(res, "project_id", "routeur_workspace.test", "default_project_id"),
			},
			{
				Config: testAccAPIKeyConfig(ws, `
  name    = "Dormant"
  enabled = false
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// A key revoked outside Terraform can never be used again: the next plan
// replaces it, and the revoked key is deleted rather than left behind.
func TestAccAPIKey_revokedOutsideTerraform(t *testing.T) {
	ws := testAccSlug("ws")
	const res = "routeur_api_key.test"
	cfg := testAccAPIKeyConfig(ws, `name = "Revoked elsewhere"`)
	var revokedPath string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckAPIKeyGone(t),
			func(*terraform.State) error {
				c, _ := testAccClient(t)
				if err := c.Do(context.Background(), http.MethodGet, revokedPath, nil, nil); !client.IsNotFound(err) {
					return fmt.Errorf("the revoked key %s should have been deleted on replacement: %v", revokedPath, err)
				}
				return nil
			},
		),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					a := s.RootModule().Resources[res].Primary.Attributes
					c, org := testAccClient(t)
					revokedPath = client.Path("orgs", org, "workspaces", a["workspace_id"], "api-keys", a["id"])
					return c.Do(context.Background(), http.MethodPost, revokedPath+"/revoke", nil, nil)
				},
				// The refresh after this step sees the revoked key.
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("state"), knownvalue.StringExact("active")),
				},
			},
			{
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestAccAPIKey_validation(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "routeur_api_key" "test" {
  workspace_id = "ws_1"
  project_id   = "checkout"
  name         = "Checkout"
}`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`must be a routeur.ai id such as proj_12`),
			},
			{
				Config: `
resource "routeur_api_key" "test" {
  workspace_id = "ws_1"
  name         = " Checkout"
}`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`must not be blank or start or end with whitespace`),
			},
		},
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_api_key",
		schema:     "ApiKeyWithSecret",
		operations: []string{"createApiKey", "getApiKey", "updateApiKey", "deleteApiKey"},
		aliases:    map[string]string{"key": "secret"},
		// last_used_at changes on every gateway call.
		ignore: []string{"last_used_at"},
	})
}
