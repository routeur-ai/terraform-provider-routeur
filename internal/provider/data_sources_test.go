package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestAccMeDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `data "routeur_me" "current" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.routeur_me.current", tfjsonpath.New("organisation").AtMapKey("slug"), knownvalue.StringExact(os.Getenv(EnvOrg))),
				statecheck.ExpectKnownValue("data.routeur_me.current", tfjsonpath.New("user").AtMapKey("id"), knownvalue.StringRegexp(regexp.MustCompile(`^user_[1-9][0-9]*$`))),
				statecheck.ExpectKnownValue("data.routeur_me.current", tfjsonpath.New("token").AtMapKey("fingerprint"), knownvalue.StringRegexp(regexp.MustCompile(`^rtm_…`))),
				statecheck.ExpectKnownValue("data.routeur_me.current", tfjsonpath.New("token").AtMapKey("expires_at"), knownvalue.Null()),
			},
		}},
	})
}

func TestAccWorkspaceAndProjectDataSources(t *testing.T) {
	ws := testAccSlug("ws")
	config := fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug        = %q
  name        = "Data sources"
  description = "Looked up"
}

data "routeur_workspace" "by_slug" {
  slug = routeur_workspace.test.slug
}

data "routeur_workspace" "by_id" {
  id = routeur_workspace.test.id
}

# The workspace's default project, created with it.
data "routeur_project" "default_by_slug" {
  workspace_id = routeur_workspace.test.id
  slug         = "${routeur_workspace.test.slug}-core"
}

data "routeur_project" "default_by_id" {
  workspace_id = routeur_workspace.test.id
  id           = routeur_workspace.test.default_project_id
}
`, ws)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWorkspaceGone(t),
		Steps: []resource.TestStep{{
			Config: config,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrPair("data.routeur_workspace.by_slug", "id", "routeur_workspace.test", "id"),
				resource.TestCheckResourceAttrPair("data.routeur_workspace.by_id", "slug", "routeur_workspace.test", "slug"),
				resource.TestCheckResourceAttr("data.routeur_workspace.by_id", "description", "Looked up"),
				resource.TestCheckResourceAttrPair("data.routeur_workspace.by_slug", "default_project_id", "routeur_workspace.test", "default_project_id"),
				resource.TestCheckResourceAttrPair("data.routeur_project.default_by_slug", "id", "routeur_workspace.test", "default_project_id"),
				resource.TestCheckResourceAttr("data.routeur_project.default_by_slug", "is_default", "true"),
				resource.TestCheckResourceAttr("data.routeur_project.default_by_slug", "enabled", "true"),
				resource.TestCheckResourceAttrPair("data.routeur_project.default_by_id", "slug", "data.routeur_project.default_by_slug", "slug"),
			),
		}},
	})
}

func TestAccDataSources_errors(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      `data "routeur_workspace" "x" {}`,
				ExpectError: regexp.MustCompile(`(?s)No attribute specified.*id.*slug`),
			},
			{
				Config:      `data "routeur_workspace" "x" { slug = "tfacc-does-not-exist" }`,
				ExpectError: regexp.MustCompile(`(?s)Could not read workspace tfacc-does-not-exist.*not_found`),
			},
			{
				Config: `
data "routeur_workspace" "default" { slug = "default" }
data "routeur_project" "x" {
  workspace_id = data.routeur_workspace.default.id
  slug         = "tfacc-does-not-exist"
}`,
				ExpectError: regexp.MustCompile(`has no project with slug "tfacc-does-not-exist"`),
			},
		},
	})
}

func init() {
	registerSpecCoverage(specCoverage{typeName: "data.routeur_me", schema: "Me", operations: []string{"getMe"}})
	registerSpecCoverage(specCoverage{typeName: "data.routeur_workspace", schema: "Workspace", operations: []string{"getWorkspace"}})
	registerSpecCoverage(specCoverage{typeName: "data.routeur_project", schema: "Project", operations: []string{"getProject", "listProjects"}})
}

// A wrong org is one provider error, and the state is untouched.
func TestAccProviderOrgMismatch(t *testing.T) {
	ws := testAccSlug("ws")
	resources := fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug = %q
  name = "Org mismatch"
}
`, ws)
	wrongOrg := `
provider "routeur" {
  org = "tfacc-not-this-org"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: resources},
			{
				Config:      wrongOrg + resources,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`token belongs to organisation\s+"` + regexp.QuoteMeta(os.Getenv(EnvOrg)) + `",\s+not\s+"tfacc-not-this-org"`),
			},
			{
				// Back on the right org: nothing was lost from state.
				Config: resources,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
