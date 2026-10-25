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
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// testAccDlpConfig is a workspace, a project and one policy; body is the
// policy's arguments besides workspace_id.
func testAccDlpConfig(wsSlug, body string) string {
	return fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug = %q
  name = "DLP tests"
}

resource "routeur_project" "test" {
  workspace_id = routeur_workspace.test.id
  slug         = "${routeur_workspace.test.slug}-scoped" # project slugs are unique per organisation
  name         = "Scoped"
}

resource "routeur_dlp_policy" "test" {
  workspace_id = routeur_workspace.test.id
  %s
}
`, wsSlug, body)
}

func testAccCheckDlpPolicyGone(t *testing.T) resource.TestCheckFunc {
	return testAccCheckGone(t, "routeur_dlp_policy", func(org string, a map[string]string) string {
		return client.Path("orgs", org, "workspaces", a["workspace_id"], "dlp-policies", a["id"])
	})
}

func TestAccDlpPolicy_basic(t *testing.T) {
	ws := testAccSlug("ws")
	const res = "routeur_dlp_policy.test"
	update := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeTestCheckFunc(testAccCheckDlpPolicyGone(t), testAccCheckWorkspaceGone(t)),
		Steps: []resource.TestStep{
			// Keyword detector: a set, so order carries no meaning.
			{
				Config: testAccDlpConfig(ws, `
  slug     = "internal_codenames"
  name     = "Internal codenames"
  detector = "keyword"
  keywords = ["Zeta", "alpha", "Beta"]
  action   = "redact"
  severity = "medium"
`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^dlp_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("project_id"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("keywords"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("Zeta"), knownvalue.StringExact("alpha"), knownvalue.StringExact("Beta"),
					})),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("pattern"), knownvalue.Null()),
				},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
			// Reordering the keywords is no change.
			{
				Config: testAccDlpConfig(ws, `
  slug     = "internal_codenames"
  name     = "Internal codenames"
  detector = "keyword"
  keywords = ["Beta", "Zeta", "alpha"]
  action   = "redact"
  severity = "medium"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			// Edit the list in place, scope to a project, change the slug (mutable).
			{
				Config: testAccDlpConfig(ws, `
  slug       = "internal_names"
  name       = "Internal codenames"
  project_id = routeur_project.test.id
  detector   = "keyword"
  keywords   = ["Beta", "Zeta"]
  action     = "block"
  severity   = "high"
  enabled    = false
`),
				ConfigPlanChecks: update,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("slug"), knownvalue.StringExact("internal_names")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("keywords"), knownvalue.SetSizeExact(2)),
				},
			},
			// Switch detector to regex: same policy, payload swapped, scope cleared.
			{
				Config: testAccDlpConfig(ws, `
  slug     = "internal_names"
  name     = "Internal codenames"
  detector = "regex"
  pattern  = "\\bPROJECT-[A-Z]{3}\\b"
  action   = "warn"
  severity = "low"
`),
				ConfigPlanChecks: update,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("pattern"), knownvalue.StringExact(`\bPROJECT-[A-Z]{3}\b`)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("keywords"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("project_id"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
				},
			},
			// Preset, then secret detectors.
			{
				Config: testAccDlpConfig(ws, `
  slug     = "internal_names"
  name     = "Internal codenames"
  detector = "preset"
  presets  = ["email", "credit_card"]
  action   = "tokenize"
  severity = "high"
`),
				ConfigPlanChecks: update,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("presets"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("email"), knownvalue.StringExact("credit_card"),
					})),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("pattern"), knownvalue.Null()),
				},
			},
			{
				Config: testAccDlpConfig(ws, `
  slug     = "internal_names"
  name     = "Internal codenames"
  detector = "secret"
  secrets  = ["adobe-client-id", "1password-secret-key"]
  action   = "log"
  severity = "low"
`),
				ConfigPlanChecks: update,
			},
			{
				Config: testAccDlpConfig(ws, `
  slug     = "internal_names"
  name     = "Internal codenames"
  detector = "secret"
  secrets  = ["adobe-client-id", "1password-secret-key"]
  action   = "log"
  severity = "low"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
		},
	})
}

func TestAccDlpPolicy_validation(t *testing.T) {
	cfg := func(body string) string {
		return `
resource "routeur_dlp_policy" "test" {
  workspace_id = "ws_1"
  slug         = "p"
  name         = "P"
  action       = "log"
  severity     = "low"
  ` + body + `
}`
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg(`detector = "regex"`), PlanOnly: true, ExpectError: regexp.MustCompile(`(?s)Missing payload for detector.*pattern.*required`)},
			{Config: cfg("detector = \"regex\"\n  pattern = \"a\"\n  keywords = [\"b\"]"), PlanOnly: true, ExpectError: regexp.MustCompile(`Payload does not match detector`)},
			{Config: cfg("detector = \"keyword\"\n  keywords = [\"a,b\"]"), PlanOnly: true, ExpectError: regexp.MustCompile(`must not contain a comma`)},
			{Config: cfg("detector = \"keyword\"\n  keywords = []"), PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)at least 1`)},
			{Config: cfg("detector = \"yara\"\n  pattern = \"a\""), PlanOnly: true, ExpectError: regexp.MustCompile(`detector value must be one of`)},
			// Not RE2 (the gateway's engine): refused at plan time.
			{Config: cfg("detector = \"regex\"\n  pattern = \"(?<=x)y\""), PlanOnly: true, ExpectError: regexp.MustCompile(`must be a valid RE2 regular expression`)},
		},
	})
}

// The API validates managed ids and regexes; its errors land on the payload attribute.
func TestAccDlpPolicy_apiValidation(t *testing.T) {
	ws := testAccSlug("ws")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDlpConfig(ws, `
  slug     = "bad_preset"
  name     = "Bad preset"
  detector = "preset"
  presets  = ["not-a-preset"]
  action   = "log"
  severity = "low"
`),
				ExpectError: regexp.MustCompile(`(?s)presets.*Unknown preset id`),
			},
		},
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_dlp_policy",
		schema:     "DlpPolicy",
		operations: []string{"createDlpPolicy", "getDlpPolicy", "replaceDlpPolicy", "deleteDlpPolicy"},
	})
}

// Payload arguments known only at apply (here from terraform_data) pass the
// detector/payload check at plan time instead of reading as "set": the
// unused `pattern` is unknown at plan and null at apply.
func TestAccDlpPolicy_payloadFromAnotherResource(t *testing.T) {
	ws := testAccSlug("ws")
	const res = "routeur_dlp_policy.test"
	cfg := `
resource "terraform_data" "keywords" {
  input = ["Zeta", "alpha"]
}

resource "terraform_data" "none" {
  input = null
}
` + testAccDlpConfig(ws, `
  slug     = "from_data"
  name     = "From data"
  detector = "keyword"
  keywords = terraform_data.keywords.output
  pattern  = terraform_data.none.output
  action   = "log"
  severity = "low"
`)
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_4_0)},
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckDlpPolicyGone(t),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("keywords"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("alpha"), knownvalue.StringExact("Zeta"),
					})),
				},
			},
			{Config: cfg, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
}
