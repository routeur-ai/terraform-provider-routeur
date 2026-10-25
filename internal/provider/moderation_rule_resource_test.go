package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// testAccUnderscoreSlug is a unique policy/rule slug, e.g. "tfacc_mod_k3j9x2".
func testAccUnderscoreSlug(prefix string) string {
	return "tfacc_" + prefix + "_" + acctest.RandStringFromCharSet(6, "abcdefghijklmnopqrstuvwxyz0123456789")
}

// testAccSafetyConfig is a workspace plus one project, with body appended.
func testAccSafetyConfig(wsSlug, body string) string {
	return fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug = %q
  name = "Safety tests"
}

resource "routeur_project" "test" {
  workspace_id = routeur_workspace.test.id
  slug         = "%s-proj"
  name         = "Scoped"
}
%s
`, wsSlug, wsSlug, body)
}

func testAccCheckModerationRuleGone(t *testing.T) resource.TestCheckFunc {
	return testAccCheckGone(t, "routeur_moderation_rule", func(org string, a map[string]string) string {
		return client.Path("orgs", org, "workspaces", a["workspace_id"], "moderation-rules", a["id"])
	})
}

func TestAccModerationRule_basic(t *testing.T) {
	ws, slug, slug2 := testAccSlug("ws"), testAccUnderscoreSlug("mod"), testAccUnderscoreSlug("mod")
	const res = "routeur_moderation_rule.test"

	cfg := func(body string) string {
		return testAccSafetyConfig(ws, `
resource "routeur_moderation_rule" "test" {
  workspace_id = routeur_workspace.test.id
`+body+`
}`)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckModerationRuleGone(t),
		Steps: []resource.TestStep{
			// Managed category; project, pattern and streaming omitted.
			{
				Config: cfg(fmt.Sprintf(`
  name     = "Profanity"
  slug     = %q
  category = "profanity"
  action   = "log"
  severity = "low"
`, slug)),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^mod_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("pattern"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("streaming"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("project_id"), knownvalue.Null()),
				},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
			// Update in place: custom regex, project scope, streaming override, rename, disable.
			{
				Config: cfg(fmt.Sprintf(`
  project_id = routeur_project.test.id
  name       = "Card numbers"
  slug       = %q
  category   = "regex"
  pattern    = "\\b\\d{16}\\b"
  action     = "block"
  severity   = "high"
  streaming  = "chunked"
  enabled    = false
`, slug2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("slug"), knownvalue.StringExact(slug2)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("pattern"), knownvalue.StringExact(`\b\d{16}\b`)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("streaming"), knownvalue.StringExact("chunked")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
				},
				Check: resource.TestCheckResourceAttrPair(res, "project_id", "routeur_project.test", "id"),
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
			// Back to a managed category with everything optional removed: the
			// pattern, streaming override and project scope are cleared and
			// enabled returns to its default.
			{
				Config: cfg(fmt.Sprintf(`
  name     = "Hate"
  slug     = %q
  category = "hate"
  action   = "redact"
  severity = "medium"
`, slug2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("pattern"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("streaming"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("project_id"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
				},
			},
			{
				Config: cfg(fmt.Sprintf(`
  name     = "Hate"
  slug     = %q
  category = "hate"
  action   = "redact"
  severity = "medium"
`, slug2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// A rule is not tied to the workspace it was created in by anything but
// workspace_id, which replaces it.
func TestAccModerationRule_replaceOnWorkspace(t *testing.T) {
	ws, ws2, slug := testAccSlug("ws"), testAccSlug("ws"), testAccUnderscoreSlug("mod")
	const res = "routeur_moderation_rule.test"
	cfg := func(target string) string {
		return fmt.Sprintf(`
resource "routeur_workspace" "a" {
  slug = %q
  name = "A"
}
resource "routeur_workspace" "b" {
  slug = %q
  name = "B"
}
resource "routeur_moderation_rule" "test" {
  workspace_id = routeur_workspace.%s.id
  name         = "Violence"
  slug         = %q
  category     = "violence"
  action       = "log"
  severity     = "low"
}`, ws, ws2, target, slug)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckModerationRuleGone(t),
		Steps: []resource.TestStep{
			{Config: cfg("a")},
			{
				Config: cfg("b"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace)},
				},
			},
		},
	})
}

func TestAccModerationRule_validation(t *testing.T) {
	base := func(body string) string {
		return `
resource "routeur_moderation_rule" "test" {
  workspace_id = "ws_1"
  name         = "Rule"
  slug         = "rule"
  action       = "log"
  severity     = "low"
` + body + `
}`
	}
	cases := []struct{ name, body, err string }{
		{"regex without pattern", `category = "regex"`, `pattern is required when category is "regex"`},
		{"managed category with pattern", "category = \"hate\"\n  pattern = \"x\"", `pattern can only be set when category is "regex"`},
		{"unknown category", `category = "spam"`, `Attribute category value must be one of`},
		{"bad streaming", "category = \"hate\"\n  streaming = \"live\"", `Attribute streaming value must be one of`},
		{"bad slug", "category = \"hate\"\n  slug = \"Bad-Slug\"", `(?s)lowercase letters and digits joined by single\s+underscores`},
		{"empty pattern", "category = \"regex\"\n  pattern = \"\"", `character count must be between 1 and 2000`},
		{"not RE2", "category = \"regex\"\n  pattern = \"(?<=x)y\"", `must be a valid RE2 regular expression`},
	}
	var steps []resource.TestStep
	for _, c := range cases {
		body := c.body
		if strings.Contains(body, "slug = ") {
			// A second slug would be a duplicate argument; drop the base one.
			steps = append(steps, resource.TestStep{
				Config:      strings.Replace(base(body), "  slug         = \"rule\"\n", "", 1),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(c.err),
			})
			continue
		}
		steps = append(steps, resource.TestStep{Config: base(body), PlanOnly: true, ExpectError: regexp.MustCompile(c.err)})
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    steps,
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_moderation_rule",
		schema:     "ModerationRule",
		operations: []string{"createModerationRule", "getModerationRule", "replaceModerationRule", "deleteModerationRule"},
	})
}
