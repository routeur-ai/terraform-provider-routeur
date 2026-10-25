package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func testAccCheckPromptShieldRuleGone(t *testing.T) resource.TestCheckFunc {
	return testAccCheckGone(t, "routeur_prompt_shield_rule", func(org string, a map[string]string) string {
		return client.Path("orgs", org, "workspaces", a["workspace_id"], "prompt-shield-rules", a["id"])
	})
}

func TestAccPromptShieldRule_basic(t *testing.T) {
	ws, slug, slug2 := testAccSlug("ws"), testAccUnderscoreSlug("shield"), testAccUnderscoreSlug("shield")
	const res = "routeur_prompt_shield_rule.test"
	cfg := func(body string) string {
		return testAccSafetyConfig(ws, `
resource "routeur_prompt_shield_rule" "test" {
  workspace_id = routeur_workspace.test.id
`+body+`
}`)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckPromptShieldRuleGone(t),
		Steps: []resource.TestStep{
			// Keyword detector: a set, so order carries no meaning.
			{
				Config: cfg(fmt.Sprintf(`
  name     = "Ignore previous"
  slug     = %q
  category = "instruction_override"
  detector = "keyword"
  keywords = ["ignore previous instructions", "disregard the above", "Forget Everything"]
  action   = "block"
  severity = "high"
`, slug)),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^shield_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("pattern"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("keywords"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("ignore previous instructions"),
						knownvalue.StringExact("disregard the above"),
						knownvalue.StringExact("Forget Everything"),
					})),
				},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
			// Reordering the keywords is no change.
			{
				Config: cfg(fmt.Sprintf(`
  name     = "Ignore previous"
  slug     = %q
  category = "instruction_override"
  detector = "keyword"
  keywords = ["Forget Everything", "disregard the above", "ignore previous instructions"]
  action   = "block"
  severity = "high"
`, slug)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			// Edit the keywords in place.
			{
				Config: cfg(fmt.Sprintf(`
  name     = "Ignore previous"
  slug     = %q
  category = "instruction_override"
  detector = "keyword"
  keywords = ["Forget Everything", "ignore previous instructions"]
  action   = "block"
  severity = "high"
`, slug)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("keywords"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("Forget Everything"),
						knownvalue.StringExact("ignore previous instructions"),
					})),
				},
			},
			// Switch detector to regex, scope to a project, rename, disable.
			{
				Config: cfg(fmt.Sprintf(`
  project_id = routeur_project.test.id
  name       = "System prompt leak"
  slug       = %q
  category   = "system_leak"
  detector   = "regex"
  pattern    = "(?i)reveal (your )?system prompt"
  action     = "warn"
  severity   = "medium"
  enabled    = false
`, slug2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("keywords"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("pattern"), knownvalue.StringExact("(?i)reveal (your )?system prompt")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
				},
				Check: resource.TestCheckResourceAttrPair(res, "project_id", "routeur_project.test", "id"),
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
			// Defaults return; project scope is cleared.
			{
				Config: cfg(fmt.Sprintf(`
  name     = "System prompt leak"
  slug     = %q
  category = "system_leak"
  detector = "regex"
  pattern  = "(?i)reveal (your )?system prompt"
  action   = "warn"
  severity = "medium"
`, slug2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("project_id"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
				},
			},
			{
				Config: cfg(fmt.Sprintf(`
  name     = "System prompt leak"
  slug     = %q
  category = "system_leak"
  detector = "regex"
  pattern  = "(?i)reveal (your )?system prompt"
  action   = "warn"
  severity = "medium"
`, slug2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

func TestAccPromptShieldRule_validation(t *testing.T) {
	base := func(body string) string {
		return `
resource "routeur_prompt_shield_rule" "test" {
  workspace_id = "ws_1"
  name         = "Rule"
  slug         = "rule"
  category     = "role_play"
  action       = "log"
  severity     = "low"
` + body + `
}`
	}
	cases := []struct{ body, err string }{
		{"detector = \"keyword\"", `keywords is required when detector is "keyword"`},
		{"detector = \"keyword\"\n  keywords = [\"a\"]\n  pattern = \"x\"", `pattern can only be set when detector is "regex"`},
		{"detector = \"regex\"", `pattern is required when detector is "regex"`},
		{"detector = \"regex\"\n  pattern = \"(?<=x)y\"", `must be a valid RE2 regular expression`},
		{"detector = \"regex\"\n  pattern = \"x\"\n  keywords = [\"a\"]", `keywords can only be set when detector is "keyword"`},
		{"detector = \"keyword\"\n  keywords = [\"a,b\"]", `must not contain a comma`},
		{"detector = \"keyword\"\n  keywords = [\" a\"]", `must not be blank or start or end with whitespace`},
		{"detector = \"keyword\"\n  keywords = []", `set must contain at least 1 elements`},
		{"detector = \"keyword\"\n  keywords = [\"" + strings.Repeat("a", 1001) + "\", \"" + strings.Repeat("b", 1000) + "\"]", `at most 2000 bytes`},
		// 2000 bytes, not characters: 667 three-byte characters plus a comma and one more are too long.
		{"detector = \"keyword\"\n  keywords = [\"" + strings.Repeat("€", 667) + "\", \"b\"]", `at most 2000 bytes`},
	}
	var steps []resource.TestStep
	for _, c := range cases {
		steps = append(steps, resource.TestStep{Config: base(c.body), PlanOnly: true, ExpectError: regexp.MustCompile(c.err)})
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    steps,
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_prompt_shield_rule",
		schema:     "PromptShieldRule",
		operations: []string{"createPromptShieldRule", "getPromptShieldRule", "replacePromptShieldRule", "deletePromptShieldRule"},
	})
}

// Payload arguments known only at apply pass the detector/payload check at
// plan time instead of reading as "set": the unused `keywords` is unknown
// at plan and null at apply.
func TestAccPromptShieldRule_patternFromAnotherResource(t *testing.T) {
	ws, slug := testAccSlug("ws"), testAccUnderscoreSlug("shield")
	const res = "routeur_prompt_shield_rule.test"
	cfg := testAccSafetyConfig(ws, fmt.Sprintf(`
resource "terraform_data" "pattern" {
  input = "(?i)ignore (all )?previous"
}

resource "terraform_data" "none" {
  input = null
}

resource "routeur_prompt_shield_rule" "test" {
  workspace_id = routeur_workspace.test.id
  name         = "From data"
  slug         = %q
  category     = "instruction_override"
  detector     = "regex"
  pattern      = terraform_data.pattern.output
  keywords     = terraform_data.none.output
  action       = "log"
  severity     = "low"
}`, slug))
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_4_0)},
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckPromptShieldRuleGone(t),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("pattern"), knownvalue.StringExact("(?i)ignore (all )?previous")),
				},
			},
			{Config: cfg, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
}
