package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// testAccProjectConfig is a workspace plus one project; body is the
// project's arguments besides workspace_id.
func testAccProjectConfig(wsSlug, body string) string {
	return fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug = %q
  name = "Project tests"
}

resource "routeur_project" "test" {
  workspace_id = routeur_workspace.test.id
  %s
}
`, wsSlug, body)
}

func testAccCheckProjectGone(t *testing.T) resource.TestCheckFunc {
	return testAccCheckGone(t, "routeur_project", func(org string, a map[string]string) string {
		return client.Path("orgs", org, "workspaces", a["workspace_id"], "projects", a["id"])
	})
}

func TestAccProject_basic(t *testing.T) {
	ws, slug, slug2 := testAccSlug("ws"), testAccSlug("proj"), testAccSlug("proj")
	const res = "routeur_project.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckProjectGone(t),
			testAccCheckWorkspaceGone(t),
		),
		Steps: []resource.TestStep{
			// Create; enabled defaults to true on the server.
			{
				Config: testAccProjectConfig(ws, fmt.Sprintf(`
  slug        = %q
  name        = "Checkout"
  description = "Checkout service"
`, slug)),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^proj_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("is_default"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("description"), knownvalue.StringExact("Checkout service")),
				},
				Check: resource.TestCheckResourceAttrPair(res, "workspace_id", "routeur_workspace.test", "id"),
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
			// Update in place: rename, disable, clear the description.
			{
				Config: testAccProjectConfig(ws, fmt.Sprintf(`
  slug    = %q
  name    = "Checkout v2"
  enabled = false
`, slug)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("name"), knownvalue.StringExact("Checkout v2")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("description"), knownvalue.Null()),
				},
			},
			// Removing `enabled` returns it to the default: the project is enabled again.
			{
				Config: testAccProjectConfig(ws, fmt.Sprintf(`
  slug = %q
  name = "Checkout v2"
`, slug)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
				},
			},
			// slug is create-only: changing it replaces the project.
			{
				Config: testAccProjectConfig(ws, fmt.Sprintf(`
  slug    = %q
  name    = "Checkout v2"
  enabled = true
`, slug2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("slug"), knownvalue.StringExact(slug2)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
				},
			},
			{
				Config: testAccProjectConfig(ws, fmt.Sprintf(`
  slug    = %q
  name    = "Checkout v2"
  enabled = true
`, slug2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// Created disabled from the start: enabled = false is sent on create.
func TestAccProject_createDisabled(t *testing.T) {
	ws, slug := testAccSlug("ws"), testAccSlug("proj")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckProjectGone(t),
		Steps: []resource.TestStep{{
			Config: testAccProjectConfig(ws, fmt.Sprintf(`
  slug    = %q
  name    = "Dormant"
  enabled = false
`, slug)),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("routeur_project.test", tfjsonpath.New("enabled"), knownvalue.Bool(false)),
			},
		}},
	})
}

func TestAccProject_validation(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "routeur_project" "test" {
  workspace_id = "default"
  slug         = "checkout"
  name         = "Checkout"
}`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`must be a routeur.ai id such as ws_12`),
			},
			{
				Config: `
resource "routeur_project" "test" {
  workspace_id = "ws_1"
  slug         = "-checkout"
  name         = "Checkout"
}`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`must be lowercase letters, digits and hyphens`),
			},
		},
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_project",
		schema:     "Project",
		operations: []string{"createProject", "getProject", "updateProject", "deleteProject"},
	})
}
