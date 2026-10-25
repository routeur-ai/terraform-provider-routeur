package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func testAccWorkspaceConfig(slug, name, descriptionLine string) string {
	return fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug = %q
  name = %q
  %s
}
`, slug, name, descriptionLine)
}

func testAccCheckWorkspaceGone(t *testing.T) resource.TestCheckFunc {
	return testAccCheckGone(t, "routeur_workspace", func(org string, a map[string]string) string {
		return client.Path("orgs", org, "workspaces", a["id"])
	})
}

func TestAccWorkspace_basic(t *testing.T) {
	slug, slug2 := testAccSlug("ws"), testAccSlug("ws")
	const res = "routeur_workspace.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWorkspaceGone(t),
		Steps: []resource.TestStep{
			// Create.
			{
				Config: testAccWorkspaceConfig(slug, "Acceptance", `description = "Created by Terraform"`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^ws_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("slug"), knownvalue.StringExact(slug)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("description"), knownvalue.StringExact("Created by Terraform")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("default_project_id"), knownvalue.StringRegexp(regexp.MustCompile(`^proj_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("is_default"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("created_at"), knownvalue.NotNull()),
				},
			},
			// Import by id, and by slug.
			{ResourceName: res, ImportState: true, ImportStateVerify: true},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateId: slug},
			// Update in place; dropping description clears it.
			{
				Config: testAccWorkspaceConfig(slug, "Acceptance renamed", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("name"), knownvalue.StringExact("Acceptance renamed")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("description"), knownvalue.Null()),
				},
			},
			// "" is stored verbatim, not turned into null (no drift).
			{
				Config: testAccWorkspaceConfig(slug, "Acceptance renamed", `description = ""`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("description"), knownvalue.StringExact("")),
				},
			},
			// slug is create-only: changing it replaces the workspace. Free text
			// keeps its surrounding whitespace.
			{
				Config: testAccWorkspaceConfig(slug2, "Acceptance renamed", `description = "  kept verbatim  "`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("slug"), knownvalue.StringExact(slug2)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("description"), knownvalue.StringExact("  kept verbatim  ")),
				},
			},
			// No drift.
			{
				Config: testAccWorkspaceConfig(slug2, "Acceptance renamed", `description = "  kept verbatim  "`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestAccWorkspace_validation(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      testAccWorkspaceConfig("Not_A_Slug", "x", ""),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`must be lowercase letters, digits and hyphens`),
			},
			{
				Config:      testAccWorkspaceConfig(testAccSlug("ws"), " padded", ""),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`must not be blank or start or end with whitespace`),
			},
		},
	})
}

// Name lengths count characters as the API does (80 two-byte characters
// are fine, 81 are not), and "" is a single error.
func TestAccWorkspace_nameCharacters(t *testing.T) {
	slug := testAccSlug("ws")
	long := strings.Repeat("é", 80)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWorkspaceGone(t),
		Steps: []resource.TestStep{
			{
				Config:      testAccWorkspaceConfig(slug, long+"é", ""),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`character count must be at most 80, got: 81`),
			},
			{
				Config:      testAccWorkspaceConfig(slug, "", ""),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`must not be blank`),
			},
			{
				Config: testAccWorkspaceConfig(slug, long, ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("routeur_workspace.test", tfjsonpath.New("name"), knownvalue.StringExact(long)),
				},
			},
			{
				Config:           testAccWorkspaceConfig(slug, long, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

// A slug the API refuses (already taken) comes back as an error on the slug
// attribute, carrying the API's own message.
func TestAccWorkspace_apiValidationError(t *testing.T) {
	slug := testAccSlug("ws")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWorkspaceGone(t),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "routeur_workspace" "a" {
  slug = %[1]q
  name = "First"
}
resource "routeur_workspace" "b" {
  slug       = %[1]q
  name       = "Second"
  depends_on = [routeur_workspace.a]
}
`, slug),
				ExpectError: regexp.MustCompile(`(?s)Could not create workspace.*slug.*taken.*validation_failed`),
			},
		},
	})
}

// Deleting a workspace that still holds something is refused by the API;
// the error must say why and what to do.
func TestAccWorkspace_deleteRefused(t *testing.T) {
	slug := testAccSlug("ws")
	var workspaceID, strayProject string
	config := testAccWorkspaceConfig(slug, "Delete refusal", "")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWorkspaceGone(t),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					func(s *terraform.State) error {
						workspaceID = s.RootModule().Resources["routeur_workspace.test"].Primary.ID
						// A project Terraform does not manage blocks the delete.
						c, org := testAccClient(t)
						p, err := client.Send[apiProject](context.Background(), c, http.MethodPost,
							client.Path("orgs", org, "workspaces", workspaceID, "projects"),
							map[string]any{"name": "Out of band", "slug": testAccSlug("stray")})
						strayProject = p.ID
						return err
					},
				),
			},
			{
				Config:      config,
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)Could not delete workspace.*workspace_not_empty`),
			},
			{
				PreConfig: func() {
					c, org := testAccClient(t)
					if err := c.Do(context.Background(), http.MethodDelete, client.Path("orgs", org, "workspaces", workspaceID, "projects", strayProject), nil, nil); err != nil {
						t.Fatal(err)
					}
				},
				Config: config,
			},
		},
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_workspace",
		schema:     "Workspace",
		operations: []string{"createWorkspace", "getWorkspace", "updateWorkspace", "deleteWorkspace"},
	})
}
