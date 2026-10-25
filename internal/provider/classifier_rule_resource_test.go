package provider

import (
	"context"
	"fmt"
	"net/http"
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

func testAccClassifierConfig(wsSlug, body string) string {
	return fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug = %q
  name = "Classifier tests"
}

resource "routeur_project" "test" {
  workspace_id = routeur_workspace.test.id
  slug         = "${routeur_workspace.test.slug}-scoped" # project slugs are unique per organisation
  name         = "Scoped"
}

resource "routeur_classifier_rule" "test" {
  workspace_id = routeur_workspace.test.id
  %s
}
`, wsSlug, body)
}

func testAccCheckClassifierRuleGone(t *testing.T) resource.TestCheckFunc {
	return testAccCheckGone(t, "routeur_classifier_rule", func(org string, a map[string]string) string {
		return client.Path("orgs", org, "workspaces", a["workspace_id"], "classifier-rules", a["id"])
	})
}

func TestAccClassifierRule_basic(t *testing.T) {
	ws := testAccSlug("ws")
	const res = "routeur_classifier_rule.test"
	update := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeTestCheckFunc(testAccCheckClassifierRuleGone(t), testAccCheckWorkspaceGone(t)),
		Steps: []resource.TestStep{
			{
				Config: testAccClassifierConfig(ws, `
  slug      = "no_weapons"
  name      = "No weapons"
  axis      = "weapons"
  min_score = 0.8571
  action    = "block"
`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^clf_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("min_score"), knownvalue.Float64Exact(0.8571)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("project_id"), knownvalue.Null()),
				},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
			// Everything is changeable in place, slug included.
			{
				Config: testAccClassifierConfig(ws, `
  slug       = "flag_weapons"
  name       = "Flag weapons"
  project_id = routeur_project.test.id
  axis       = "violence"
  min_score  = 1
  action     = "flag"
  enabled    = false
`),
				ConfigPlanChecks: update,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("slug"), knownvalue.StringExact("flag_weapons")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("min_score"), knownvalue.Float64Exact(1)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
				},
			},
			// Removing project_id and enabled returns to workspace scope and enabled.
			{
				Config: testAccClassifierConfig(ws, `
  slug      = "flag_weapons"
  name      = "Flag weapons"
  axis      = "violence"
  min_score = 0
  action    = "log"
`),
				ConfigPlanChecks: update,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("project_id"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("min_score"), knownvalue.Float64Exact(0)),
				},
			},
			{
				Config: testAccClassifierConfig(ws, `
  slug      = "flag_weapons"
  name      = "Flag weapons"
  axis      = "violence"
  min_score = 0
  action    = "log"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

func TestAccClassifierRule_validation(t *testing.T) {
	cfg := func(axis, score, action string) string {
		return fmt.Sprintf(`
resource "routeur_classifier_rule" "test" {
  workspace_id = "ws_1"
  slug         = "r"
  name         = "R"
  axis         = %q
  min_score    = %s
  action       = %q
}`, axis, score, action)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg("toxicity", "1.5", "log"), PlanOnly: true, ExpectError: regexp.MustCompile(`(?s)must be between 0\.0+ and 1\.0+`)},
			{Config: cfg("sarcasm", "0.5", "log"), PlanOnly: true, ExpectError: regexp.MustCompile(`axis value must be one of`)},
			{Config: cfg("toxicity", "0.5", "ban"), PlanOnly: true, ExpectError: regexp.MustCompile(`action value must be one of`)},
		},
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_classifier_rule",
		schema:     "ClassifierRule",
		operations: []string{"createClassifierRule", "getClassifierRule", "replaceClassifierRule", "deleteClassifierRule"},
	})
}

// Decimals that float64 cannot represent exactly (0.755, 2.755) import
// without a phantom in-place update: the provider keeps the API's decimal
// at the precision Terraform parses configuration with.
func TestAccImportDecimals(t *testing.T) {
	testAccSkipUnlessAcc(t)
	ctx := context.Background()
	c, org := testAccClient(t)
	ws, err := client.Send[apiWorkspace](ctx, c, http.MethodPost, client.Path("orgs", org, "workspaces"), map[string]any{"slug": testAccSlug("ws"), "name": "Decimals"})
	if err != nil {
		t.Fatalf("creating workspace: %v", err)
	}
	t.Cleanup(func() { _ = c.Do(ctx, http.MethodDelete, client.Path("orgs", org, "workspaces", ws.ID), nil, nil) })
	clf, err := client.Send[apiClassifierRule](ctx, c, http.MethodPost, client.Path("orgs", org, "workspaces", ws.ID, "classifier-rules"),
		map[string]any{"slug": "decimal", "name": "Decimal", "axis": "toxicity", "min_score": 0.755, "action": "log"})
	if err != nil {
		t.Fatalf("creating classifier rule: %v", err)
	}
	anom, err := client.Send[apiAnomalyDetector](ctx, c, http.MethodPost, client.Path("orgs", org, "workspaces", ws.ID, "anomaly-detectors"),
		map[string]any{"name": "Decimal", "type": "error_burst", "threshold": map[string]any{"error_rate_pct": 2.755}, "window_minutes": 15, "action": "alert"})
	if err != nil {
		t.Fatalf("creating anomaly detector: %v", err)
	}

	config := fmt.Sprintf(`
resource "routeur_classifier_rule" "test" {
  workspace_id = %[1]q
  slug         = "decimal"
  name         = "Decimal"
  axis         = "toxicity"
  min_score    = 0.755
  action       = "log"
}

resource "routeur_anomaly_detector" "test" {
  workspace_id   = %[1]q
  name           = "Decimal"
  type           = "error_burst"
  threshold      = { error_rate_pct = 2.755 }
  window_minutes = 15
  action         = "alert"
}
`, ws.ID)
	empty := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_7_0)},
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeTestCheckFunc(testAccCheckClassifierRuleGone(t), testAccCheckAnomalyDetectorGone(t)),
		Steps: []resource.TestStep{
			// `terraform import`, then plan.
			{Config: config, ResourceName: "routeur_classifier_rule.test", ImportState: true, ImportStateId: ws.ID + "/" + clf.ID, ImportStatePersist: true},
			{Config: config, ResourceName: "routeur_anomaly_detector.test", ImportState: true, ImportStateId: ws.ID + "/" + anom.ID, ImportStatePersist: true},
			{Config: config, ConfigPlanChecks: empty},
			// Config-driven import: the imported values meet the configuration
			// in the same plan, without a round trip through the state file.
			{Config: `
removed {
  from = routeur_classifier_rule.test
  lifecycle { destroy = false }
}
removed {
  from = routeur_anomaly_detector.test
  lifecycle { destroy = false }
}
`},
			{
				Config: config + fmt.Sprintf(`
import {
  to = routeur_classifier_rule.test
  id = "%[1]s/%[2]s"
}
import {
  to = routeur_anomaly_detector.test
  id = "%[1]s/%[3]s"
}
`, ws.ID, clf.ID, anom.ID),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("routeur_classifier_rule.test", plancheck.ResourceActionNoop),
					plancheck.ExpectResourceAction("routeur_anomaly_detector.test", plancheck.ResourceActionNoop),
				}},
			},
			{Config: config, ConfigPlanChecks: empty},
		},
	})
}
