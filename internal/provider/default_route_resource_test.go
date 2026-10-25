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

func testAccDefaultRouteConfig(wsSlug, body string) string {
	return fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug = %q
  name = "Default route tests"
}

resource "routeur_default_route" "test" {
  workspace_id = routeur_workspace.test.id
  %s
}
`, wsSlug, body)
}

// testAccDefaultRouteImportID is the workspace id, the Default route's import id.
func testAccDefaultRouteImportID(s *terraform.State) (string, error) {
	rs, ok := s.RootModule().Resources["routeur_default_route.test"]
	if !ok {
		return "", fmt.Errorf("routeur_default_route.test not in state")
	}
	return rs.Primary.Attributes["workspace_id"], nil
}

func TestAccDefaultRoute_basic(t *testing.T) {
	ws := testAccSlug("ws")
	const res = "routeur_default_route.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWorkspaceGone(t),
		Steps: []resource.TestStep{
			{
				Config: testAccDefaultRouteConfig(ws, `quality_bias = 80`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^route_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("quality_bias"), knownvalue.Int64Exact(80)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("slug"), knownvalue.StringExact("route_default")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("name"), knownvalue.StringExact("Default")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("priority"), knownvalue.Int64Exact(1000)),
				},
				Check: resource.TestCheckResourceAttrPair(res, "workspace_id", "routeur_workspace.test", "id"),
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccDefaultRouteImportID},
			{
				Config: testAccDefaultRouteConfig(ws, `quality_bias = 0`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("quality_bias"), knownvalue.Int64Exact(0)),
				},
			},
			// Removing the argument returns it to the neutral 50.
			{
				Config: testAccDefaultRouteConfig(ws, ``),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("quality_bias"), knownvalue.Int64Exact(50)),
				},
			},
			{
				Config: testAccDefaultRouteConfig(ws, ``),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// Destroy cannot delete the Default route: it sets the bias back to 50.
// The workspace lives outside the configuration so it outlives the destroy.
func TestAccDefaultRoute_destroyResets(t *testing.T) {
	ws := testAccRoutingWorkspace(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			c, org := testAccClient(t)
			rule, err := client.Get[apiRoutingRule](context.Background(), c, client.Path("orgs", org, "workspaces", ws, "default-route"))
			if err != nil {
				return err
			}
			if rule.Action.QualityBias == nil || *rule.Action.QualityBias != defaultQualityBias {
				return fmt.Errorf("Default route of %s: quality_bias %v after destroy, want %d", ws, rule.Action.QualityBias, defaultQualityBias)
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
resource "routeur_default_route" "test" {
  workspace_id = %q
  quality_bias = 90
}`, ws),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("routeur_default_route.test", tfjsonpath.New("quality_bias"), knownvalue.Int64Exact(90)),
			},
		}},
	})
}

func TestAccDefaultRoute_validation(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "routeur_default_route" "test" {
  workspace_id = "ws_1"
  quality_bias = 101
}`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`value must be between 0 and 100`),
			},
			{
				Config: `
resource "routeur_default_route" "test" {
  workspace_id = "default"
}`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`must be a routeur.ai id such as ws_12`),
			},
		},
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_default_route",
		schema:     "RoutingRule",
		operations: []string{"getDefaultRoute", "replaceDefaultRoute"},
		// action is always {quality_bias} on the Default route.
		aliases: map[string]string{"action": "quality_bias"},
		// Fixed on the Default route: every project, enabled, auto, matches
		// every request, no failure policy.
		ignore: []string{"project_id", "enabled", "is_default", "action_kind", "match", "on_failure"},
	})
}
