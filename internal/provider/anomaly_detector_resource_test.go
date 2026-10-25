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

func testAccAnomalyConfig(wsSlug, body string) string {
	return fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug = %q
  name = "Anomaly tests"
}

resource "routeur_anomaly_detector" "test" {
  workspace_id = routeur_workspace.test.id
  %s
}
`, wsSlug, body)
}

func testAccCheckAnomalyDetectorGone(t *testing.T) resource.TestCheckFunc {
	return testAccCheckGone(t, "routeur_anomaly_detector", func(org string, a map[string]string) string {
		return client.Path("orgs", org, "workspaces", a["workspace_id"], "anomaly-detectors", a["id"])
	})
}

func TestAccAnomalyDetector_basic(t *testing.T) {
	ws := testAccSlug("ws")
	const res = "routeur_anomaly_detector.test"
	update := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeTestCheckFunc(testAccCheckAnomalyDetectorGone(t), testAccCheckWorkspaceGone(t)),
		Steps: []resource.TestStep{
			{
				Config: testAccAnomalyConfig(ws, `
  name           = "Daily spend"
  type           = "cost_spike"
  threshold      = { amount_cents = 50000 }
  window_minutes = 1440
  action         = "alert"
`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^anom_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("threshold").AtMapKey("amount_cents"), knownvalue.Int64Exact(50000)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("threshold").AtMapKey("p95_ms"), knownvalue.Null()),
				},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
			// In place: new threshold, window, action, disabled.
			{
				Config: testAccAnomalyConfig(ws, `
  name           = "Daily spend"
  type           = "cost_spike"
  threshold      = { amount_cents = 75000 }
  window_minutes = 360
  action         = "block"
  enabled        = false
`),
				ConfigPlanChecks: update,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("threshold").AtMapKey("amount_cents"), knownvalue.Int64Exact(75000)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
				},
			},
			// Change the type: the threshold key moves with it.
			{
				Config: testAccAnomalyConfig(ws, `
  name           = "Daily spend"
  type           = "error_burst"
  threshold      = { error_rate_pct = 2.5 }
  window_minutes = 15
  action         = "quarantine"
`),
				ConfigPlanChecks: update,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("threshold").AtMapKey("error_rate_pct"), knownvalue.Float64Exact(2.5)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("threshold").AtMapKey("amount_cents"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
				},
			},
			{
				Config: testAccAnomalyConfig(ws, `
  name           = "Daily spend"
  type           = "latency_spike"
  threshold      = { p95_ms = 4000 }
  window_minutes = 15
  action         = "alert"
`),
				ConfigPlanChecks: update,
			},
			{
				Config: testAccAnomalyConfig(ws, `
  name           = "Daily spend"
  type           = "volume_spike"
  threshold      = { baseline_pct = 300 }
  window_minutes = 15
  action         = "alert"
`),
				ConfigPlanChecks: update,
			},
			{
				Config: testAccAnomalyConfig(ws, `
  name           = "Daily spend"
  type           = "volume_spike"
  threshold      = { baseline_pct = 300 }
  window_minutes = 15
  action         = "alert"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
		},
	})
}

func TestAccAnomalyDetector_validation(t *testing.T) {
	cfg := func(typ, threshold string, window int) string {
		return fmt.Sprintf(`
resource "routeur_anomaly_detector" "test" {
  workspace_id   = "ws_1"
  name           = "D"
  type           = %q
  threshold      = %s
  window_minutes = %d
  action         = "alert"
}`, typ, threshold, window)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg("cost_spike", "{ p95_ms = 10 }", 5), PlanOnly: true, ExpectError: regexp.MustCompile(`(?s)Missing threshold for type.*amount_cents`)},
			{Config: cfg("cost_spike", "{ amount_cents = 10, p95_ms = 10 }", 5), PlanOnly: true, ExpectError: regexp.MustCompile(`Threshold does not match type`)},
			{Config: cfg("error_burst", "{ error_rate_pct = 101 }", 5), PlanOnly: true, ExpectError: regexp.MustCompile(`(?s)must be between 0\.01\d* and\s+100`)},
			{Config: cfg("latency_spike", "{ p95_ms = 0 }", 5), PlanOnly: true, ExpectError: regexp.MustCompile(`must be between 1 and 10000000`)},
			{Config: cfg("volume_spike", "{ baseline_pct = 5 }", 7), PlanOnly: true, ExpectError: regexp.MustCompile(`window_minutes value must be one of`)},
			{Config: cfg("volume_spike", "{}", 5), PlanOnly: true, ExpectError: regexp.MustCompile(`Missing threshold for type`)},
		},
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_anomaly_detector",
		schema:     "AnomalyDetector",
		operations: []string{"createAnomalyDetector", "getAnomalyDetector", "replaceAnomalyDetector", "deleteAnomalyDetector"},
	})
}

// Threshold arguments known only at apply pass the type/threshold check at
// plan time instead of reading as "set": the unused `p95_ms` is unknown at
// plan and null at apply.
func TestAccAnomalyDetector_thresholdFromAnotherResource(t *testing.T) {
	ws := testAccSlug("ws")
	const res = "routeur_anomaly_detector.test"
	cfg := `
resource "terraform_data" "limit" {
  input = 2.5
}

resource "terraform_data" "none" {
  input = null
}
` + testAccAnomalyConfig(ws, `
  name           = "Errors from data"
  type           = "error_burst"
  threshold      = { error_rate_pct = terraform_data.limit.output, p95_ms = terraform_data.none.output }
  window_minutes = 15
  action         = "alert"
`)
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_4_0)},
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAnomalyDetectorGone(t),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("threshold").AtMapKey("error_rate_pct"), knownvalue.Float64Exact(2.5)),
				},
			},
			{Config: cfg, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
}
