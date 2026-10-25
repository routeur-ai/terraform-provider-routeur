package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// testAccWorkspaceModelConfig is a workspace and `extra` (resources or data
// sources that use it). The OpenAI credential is shared between tests and
// set up out of band (testAccEnsureCredential).
func testAccWorkspaceModelConfig(wsSlug, extra string) string {
	return fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug = %q
  name = "Model tests"
}
%s
`, wsSlug, extra)
}

func testAccWorkspaceModelResource(body string) string {
	return fmt.Sprintf(`
resource "routeur_workspace_model" "test" {
  workspace_id = routeur_workspace.test.id
  %s
}
`, body)
}

// testAccCheckWorkspaceModelInactive is a CheckDestroy: destroying the
// resource deactivates the model (models are never deleted). The workspace
// is gone too, so a 404 counts as inactive.
func testAccCheckWorkspaceModelInactive(t *testing.T) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		c, org := testAccClient(t)
		for _, rs := range s.RootModule().Resources {
			if rs.Type != "routeur_workspace_model" {
				continue
			}
			a := rs.Primary.Attributes
			m, err := client.Get[apiWorkspaceModel](context.Background(), c, client.Path("orgs", org, "workspaces", a["workspace_id"], "models", a["model"]))
			if client.IsNotFound(err) {
				continue
			}
			if err != nil {
				return err
			}
			if m.Active {
				return fmt.Errorf("%s is still active after destroy", rs.Primary.ID)
			}
		}
		return nil
	}
}

func TestAccWorkspaceModel_basic(t *testing.T) {
	testAccEnsureCredential(t, testAccSharedCredentialProvider)
	ws := testAccSlug("ws")
	const res = "routeur_workspace_model.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWorkspaceModelInactive(t),
		Steps: []resource.TestStep{
			// Activate a discovered model whose id contains "/".
			{
				Config: testAccWorkspaceModelConfig(ws, testAccWorkspaceModelResource(`model = "openai:ft/custom-model"`)),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^ws_[1-9][0-9]*/openai:ft/custom-model$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("active"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("provider_name"), knownvalue.StringExact("openai")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("model_id"), knownvalue.StringExact("ft/custom-model")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("display_name"), knownvalue.StringExact("ft/custom-model")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("catalogued"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("availability"), knownvalue.StringExact("available")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("capabilities"), knownvalue.SetExact([]knownvalue.Check{})),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("context_tokens"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("input_per_1k"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("output_per_1k"), knownvalue.Null()),
				},
			},
			// Import by `<workspace_id>/<model>` (the id); the model keeps its "/".
			{ResourceName: res, ImportState: true, ImportStateVerify: true},
			// Deactivate in place.
			{
				Config: testAccWorkspaceModelConfig(ws, testAccWorkspaceModelResource(`
  model  = "openai:ft/custom-model"
  active = false
`)),
				// Only `active` changes: the catalogue metadata stays known in the
				// plan instead of showing "(known after apply)".
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("display_name"), knownvalue.StringExact("ft/custom-model")),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("catalogued"), knownvalue.Bool(false)),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("availability"), knownvalue.StringExact("available")),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("capabilities"), knownvalue.SetExact([]knownvalue.Check{})),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("context_tokens"), knownvalue.Null()),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("input_per_1k"), knownvalue.Null()),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("output_per_1k"), knownvalue.Null()),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("active"), knownvalue.Bool(false)),
				},
			},
			// Removing `active` activates it again.
			{
				Config: testAccWorkspaceModelConfig(ws, testAccWorkspaceModelResource(`model = "openai:ft/custom-model"`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("active"), knownvalue.Bool(true)),
				},
			},
			// Another model replaces the resource; a catalogued one has prices.
			{
				Config: testAccWorkspaceModelConfig(ws, testAccWorkspaceModelResource(`model = "openai:gpt-4o"`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("catalogued"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("display_name"), knownvalue.StringExact("GPT-4o")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("capabilities"), knownvalue.SetPartial([]knownvalue.Check{knownvalue.StringExact("text")})),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("context_tokens"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("input_per_1k"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("output_per_1k"), knownvalue.NotNull()),
				},
			},
			// A catalogued model's prices and capabilities stay known too.
			{
				Config: testAccWorkspaceModelConfig(ws, testAccWorkspaceModelResource(`
  model  = "openai:gpt-4o"
  active = false
`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("display_name"), knownvalue.StringExact("GPT-4o")),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("capabilities"), knownvalue.SetPartial([]knownvalue.Check{knownvalue.StringExact("text")})),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("context_tokens"), knownvalue.NotNull()),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("input_per_1k"), knownvalue.NotNull()),
						plancheck.ExpectKnownValue(res, tfjsonpath.New("output_per_1k"), knownvalue.NotNull()),
					},
				},
			},
			{
				Config: testAccWorkspaceModelConfig(ws, testAccWorkspaceModelResource(`
  model  = "openai:gpt-4o"
  active = false
`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// A model routeur.ai does not know is a clear error on `model`.
func TestAccWorkspaceModel_unknownModel(t *testing.T) {
	testAccEnsureCredential(t, testAccSharedCredentialProvider)
	ws := testAccSlug("ws")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      testAccWorkspaceModelConfig(ws, testAccWorkspaceModelResource(`model = "openai:no-such-model"`)),
			ExpectError: regexp.MustCompile(`(?s)routeur.ai does not know the model\s+"openai:no-such-model"`),
		}},
	})
}

func TestAccWorkspaceModel_validation(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
resource "routeur_workspace_model" "test" {
  workspace_id = "ws_1"
  model        = "gpt-4o"
}`,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`must be <provider>:<model_id>`),
		}},
	})
}

func TestAccWorkspaceModelsDataSource(t *testing.T) {
	testAccEnsureCredential(t, testAccSharedCredentialProvider)
	ws := testAccSlug("ws")
	cfg := testAccWorkspaceModelConfig(ws, testAccWorkspaceModelResource(`model = "openai:gpt-4o-mini"`)+`
data "routeur_workspace_models" "openai" {
  workspace_id  = routeur_workspace.test.id
  provider_name = "openai"
  depends_on    = [routeur_workspace_model.test]
}

data "routeur_workspace_models" "active" {
  workspace_id = routeur_workspace.test.id
  active       = true
  depends_on   = [routeur_workspace_model.test]
}

data "routeur_workspace_models" "none" {
  workspace_id  = routeur_workspace.test.id
  provider_name = "no-such-provider"
  depends_on    = [routeur_workspace_model.test]
}
`)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWorkspaceModelInactive(t),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.routeur_workspace_models.active", "models.#", "1"),
				resource.TestCheckResourceAttr("data.routeur_workspace_models.active", "models.0.model", "openai:gpt-4o-mini"),
				resource.TestCheckResourceAttr("data.routeur_workspace_models.active", "models.0.active", "true"),
				resource.TestCheckResourceAttr("data.routeur_workspace_models.active", "models.0.provider_name", "openai"),
				resource.TestCheckResourceAttr("data.routeur_workspace_models.active", "models.0.model_id", "gpt-4o-mini"),
				resource.TestCheckTypeSetElemNestedAttrs("data.routeur_workspace_models.openai", "models.*", map[string]string{
					"model":        "openai:ft/custom-model",
					"model_id":     "ft/custom-model",
					"catalogued":   "false",
					"active":       "false",
					"availability": "available",
				}),
				resource.TestCheckNoResourceAttr("data.routeur_workspace_models.openai", "models.0.input_per_1k"),
				resource.TestCheckResourceAttr("data.routeur_workspace_models.none", "models.#", "0"),
			),
		}},
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_workspace_model",
		schema:     "WorkspaceModel",
		operations: []string{"getWorkspaceModel", "setWorkspaceModelActive"},
		// The API's id is the model slug; the resource's id adds the
		// workspace so it is unique in state and importable.
		aliases: map[string]string{"id": "model", "provider": "provider_name"},
	})
	// The data source's entries are built from the same workspaceModelInfo
	// attributes, so the resource's coverage covers their fields too.
	registerSpecCoverage(specCoverage{
		typeName:   "data.routeur_workspace_models",
		operations: []string{"listWorkspaceModels"},
	})
}
