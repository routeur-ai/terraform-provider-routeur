package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func testAccCheckBudgetGone(t *testing.T) resource.TestCheckFunc {
	return testAccCheckGone(t, "routeur_budget", func(org string, a map[string]string) string {
		return client.Path("orgs", org, "budgets", a["id"])
	})
}

func TestAccBudget_basic(t *testing.T) {
	ws := testAccSlug("ws")
	const res = "routeur_budget.test"
	cfg := func(body string) string {
		return testAccSafetyConfig(ws, `
resource "routeur_budget" "test" {
`+body+`
}`)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBudgetGone(t),
		Steps: []resource.TestStep{
			// Organisation budget with every server default.
			{
				Config: cfg(`
  name         = "Org cap"
  scope_type   = "organisation"
  amount_cents = 50000
`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^bud_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("scope_id"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("period"), knownvalue.StringExact("monthly")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("alert_threshold_pct"), knownvalue.Int64Exact(80)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("hard_block"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("currency"), knownvalue.StringExact("USD")),
				},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true},
			// Rescope in place to the workspace, and change the numbers.
			{
				Config: cfg(`
  name                = "Workspace cap"
  scope_type          = "workspace"
  scope_id            = routeur_workspace.test.id
  period              = "weekly"
  amount_cents        = 12345
  alert_threshold_pct = 50
  hard_block          = true
  enabled             = false
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("period"), knownvalue.StringExact("weekly")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("amount_cents"), knownvalue.Int64Exact(12345)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("hard_block"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
				},
				Check: resource.TestCheckResourceAttrPair(res, "scope_id", "routeur_workspace.test", "id"),
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true},
			// Workspace to project: the type changes, so the id must too.
			{
				Config: cfg(`
  name         = "Workspace cap"
  scope_type   = "project"
  scope_id     = routeur_project.test.id
  amount_cents = 12345
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrPair(res, "scope_id", "routeur_project.test", "id"),
					resource.TestCheckResourceAttr(res, "period", "monthly"),
					resource.TestCheckResourceAttr(res, "hard_block", "false"),
					resource.TestCheckResourceAttr(res, "alert_threshold_pct", "80"),
					resource.TestCheckResourceAttr(res, "enabled", "true"),
				),
			},
			// Back to the organisation: scope_id is cleared.
			{
				Config: cfg(`
  name         = "Workspace cap"
  scope_type   = "organisation"
  amount_cents = 12345
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("scope_id"), knownvalue.Null()),
				},
			},
			{
				Config: cfg(`
  name         = "Workspace cap"
  scope_type   = "organisation"
  amount_cents = 12345
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

func TestAccBudget_validation(t *testing.T) {
	cfg := func(body string) string {
		return `
resource "routeur_budget" "test" {
  name = "Cap"
` + body + `
}`
	}
	cases := []struct{ body, err string }{
		{"scope_type = \"organisation\"\n  scope_id = \"ws_1\"\n  amount_cents = 100", `scope_id must be omitted when scope_type is "organisation"`},
		{"scope_type = \"workspace\"\n  amount_cents = 100", `scope_id is required when scope_type is "workspace"`},
		{"scope_type = \"workspace\"\n  scope_id = \"proj_1\"\n  amount_cents = 100", `scope_id must be a ws_… id`},
		{"scope_type = \"api_key\"\n  scope_id = \"ws_1\"\n  amount_cents = 100", `scope_id must be a key_… id`},
		{"scope_type = \"organisation\"\n  amount_cents = 0", `Attribute amount_cents value must be between 1 and 100000000`},
		{"scope_type = \"organisation\"\n  amount_cents = 100000001", `Attribute amount_cents value must be between 1 and 100000000`},
		{"scope_type = \"organisation\"\n  amount_cents = 100\n  alert_threshold_pct = 101", `Attribute alert_threshold_pct value must be between 1 and 100`},
		{"scope_type = \"organisation\"\n  amount_cents = 100\n  period = \"daily\"", `Attribute period value must be one of`},
		{"scope_type = \"team\"\n  amount_cents = 100", `Attribute scope_type value must be one of`},
	}
	var steps []resource.TestStep
	for _, c := range cases {
		steps = append(steps, resource.TestStep{Config: cfg(c.body), PlanOnly: true, ExpectError: regexp.MustCompile(c.err)})
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    steps,
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_budget",
		schema:     "Budget",
		operations: []string{"createBudget", "getBudget", "updateBudget", "deleteBudget"},
	})
}
