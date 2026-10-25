package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// testAccRoutingModels are activated in every routing test workspace.
var testAccRoutingModels = []string{"openai:gpt-4o", "openai:gpt-4o-mini", "openai:ft/custom-model"}

// testAccRoutingWorkspace creates a workspace out of band with
// testAccRoutingModels activated (routing rules may only name activated
// models), and deletes it when the test ends. It returns the workspace id.
func testAccRoutingWorkspace(t *testing.T) string {
	t.Helper()
	testAccSkipUnlessAcc(t)
	ctx := context.Background()
	c, org := testAccClient(t)

	testAccEnsureCredential(t, testAccSharedCredentialProvider)

	ws, err := client.Send[apiWorkspace](ctx, c, http.MethodPost, client.Path("orgs", org, "workspaces"),
		map[string]any{"slug": testAccSlug("ws"), "name": "Routing tests"})
	if err != nil {
		t.Fatalf("creating the workspace: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Do(ctx, http.MethodDelete, client.Path("orgs", org, "workspaces", ws.ID), nil, nil); err != nil && !client.IsNotFound(err) {
			t.Errorf("deleting workspace %s: %v", ws.ID, err)
		}
	})
	for _, m := range testAccRoutingModels {
		if err := c.Do(ctx, http.MethodPut, client.Path("orgs", org, "workspaces", ws.ID, "models", m), map[string]any{"active": true}, nil); err != nil {
			t.Fatalf("activating %s: %v", m, err)
		}
	}
	return ws.ID
}

func testAccCheckRoutingRuleGone(t *testing.T) resource.TestCheckFunc {
	return testAccCheckGone(t, "routeur_routing_rule", func(org string, a map[string]string) string {
		return client.Path("orgs", org, "workspaces", a["workspace_id"], "routing-rules", a["id"])
	})
}

func TestAccRoutingRule_basic(t *testing.T) {
	ws, ws2 := testAccRoutingWorkspace(t), testAccRoutingWorkspace(t)
	const res = "routeur_routing_rule.test"
	proj := testAccSlug("proj")
	config := func(wsID, body string) string {
		return fmt.Sprintf(`
resource "routeur_project" "test" {
  workspace_id = %q
  slug         = %q
  name         = "Routing target"
}

resource "routeur_routing_rule" "test" {
  workspace_id = %q
  %s
}
`, ws, proj, wsID, body)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckRoutingRuleGone(t),
		Steps: []resource.TestStep{
			// Minimal pin: name, priority and slug come from the server; a
			// model id with a slash round-trips.
			{
				Config: config(ws, `
  action = {
    pin = { provider = "openai", model = "ft/custom-model" }
  }
`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^route_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("slug"), knownvalue.StringRegexp(regexp.MustCompile(`^route_[0-9]+$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("name"), knownvalue.StringExact("Use ft/custom-model for all requests")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("priority"), knownvalue.Int64Exact(10)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("match"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("on_failure"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("project_id"), knownvalue.Null()),
				},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
			// In place: top-level conditions, auto, failure policy, project
			// scope, authored name and priority, disabled.
			{
				Config: config(ws, `
  project_id = routeur_project.test.id
  name       = "Long prompts"
  priority   = 5
  enabled    = false
  match = {
    requested_model  = "gpt-4o"
    prompt_contains  = "invoice"
    prompt_pattern   = "^(?i)total: [0-9]+ $"
    min_input_tokens = 4000
    contains_image   = true
  }
  action = {
    auto = { quality_bias = 80 }
  }
  on_failure = {
    retries      = 2
    wait_seconds = 5
    then         = "next"
  }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("name"), knownvalue.StringExact("Long prompts")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("priority"), knownvalue.Int64Exact(5)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("match").AtMapKey("prompt_pattern"), knownvalue.StringExact("^(?i)total: [0-9]+ $")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("action").AtMapKey("auto").AtMapKey("quality_bias"), knownvalue.Int64Exact(80)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("on_failure").AtMapKey("then"), knownvalue.StringExact("next")),
				},
				Check: resource.TestCheckResourceAttrPair(res, "project_id", "routeur_project.test", "id"),
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
			// Condition groups and failover; dropping project_id, on_failure,
			// enabled, name and priority: the first three return to their
			// defaults, name and priority keep their current values.
			{
				Config: config(ws, `
  match = {
    any_of = [
      { requested_model = "gpt-4o", min_input_tokens = 1000 },
      { contains_image = true },
    ]
  }
  action = {
    failover = {
      strategy = "race"
      targets = [
        { provider = "openai", model = "gpt-4o" },
        { provider = "openai", model = "gpt-4o-mini" },
      ]
    }
  }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("name"), knownvalue.StringExact("Long prompts")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("priority"), knownvalue.Int64Exact(5)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("project_id"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("on_failure"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("match").AtMapKey("requested_model"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("match").AtMapKey("any_of"), knownvalue.ListSizeExact(2)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("action").AtMapKey("failover").AtMapKey("targets").AtSliceIndex(1).AtMapKey("model"), knownvalue.StringExact("gpt-4o-mini")),
				},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportID(res)},
			// Failover strategy defaults to sequential; removing it is an update.
			{
				Config: config(ws, `
  action = {
    failover = {
      targets = [
        { provider = "openai", model = "gpt-4o-mini" },
        { provider = "openai", model = "gpt-4o" },
      ]
    }
  }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("action").AtMapKey("failover").AtMapKey("strategy"), knownvalue.StringExact("sequential")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("match"), knownvalue.Null()),
				},
			},
			// workspace_id replaces the rule.
			{
				Config: config(ws2, `
  action = {
    auto = {}
  }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("workspace_id"), knownvalue.StringExact(ws2)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("action").AtMapKey("auto").AtMapKey("quality_bias"), knownvalue.Int64Exact(50)),
				},
			},
			{
				Config: config(ws2, `
  action = {
    auto = {}
  }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// The API does not require unique priorities: rules that share one run in
// creation order, and neither shifts the other.
func TestAccRoutingRule_samePriority(t *testing.T) {
	ws := testAccRoutingWorkspace(t)
	config := fmt.Sprintf(`
resource "routeur_routing_rule" "first" {
  workspace_id = %[1]q
  priority     = 100
  match        = { prompt_contains = "refund" }
  action       = { pin = { provider = "openai", model = "gpt-4o" } }
}

resource "routeur_routing_rule" "second" {
  workspace_id = %[1]q
  priority     = 100
  match        = { prompt_contains = "invoice" }
  action       = { auto = { quality_bias = 10 } }
}
`, ws)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckRoutingRuleGone(t),
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("routeur_routing_rule.first", tfjsonpath.New("priority"), knownvalue.Int64Exact(100)),
					statecheck.ExpectKnownValue("routeur_routing_rule.second", tfjsonpath.New("priority"), knownvalue.Int64Exact(100)),
				},
			},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
}

// API validation errors land on the nested attribute they concern.
func TestAccRoutingRule_apiErrors(t *testing.T) {
	ws := testAccRoutingWorkspace(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "routeur_routing_rule" "test" {
  workspace_id = %q
  action       = { pin = { provider = "openai", model = "o3-mini" } }
}`, ws),
				ExpectError: regexp.MustCompile(`not activated in this\s+workspace`),
			},
			{
				Config: fmt.Sprintf(`
resource "routeur_routing_rule" "test" {
  workspace_id = %q
  action = {
    failover = {
      targets = [
        { provider = "openai", model = "gpt-4o" },
        { provider = "openai", model = "o3-mini" },
      ]
    }
  }
}`, ws),
				ExpectError: regexp.MustCompile(`not activated in this\s+workspace`),
			},
		},
	})
}

func TestAccRoutingRule_validation(t *testing.T) {
	rule := func(body string) string {
		return `
resource "routeur_routing_rule" "test" {
  workspace_id = "ws_1"
` + body + `
}`
	}
	auto := `  action = { auto = {} }` + "\n"
	cases := []struct{ body, err string }{
		{`  action = {}`, `(?s)No attribute specified when one \(and only one\) of`},
		{`  action = { auto = {}, pin = { provider = "openai", model = "gpt-4o" } }`, `(?s)2 attributes specified when one \(and only one\) of`},
		{auto + `  match = {}`, `set at least one condition or any_of`},
		{auto + `  match = { any_of = [{ contains_image = true }] }`, `list must contain at least 2 elements`},
		{auto + `  match = { any_of = [{ contains_image = true }, {}] }`, `a condition group needs at least one condition`},
		{auto + `  match = { requested_model = "a", any_of = [{ contains_image = true }, { prompt_contains = "b" }] }`, `(?s)cannot be specified when`},
		{auto + `  match = { contains_image = false }`, `Value must be "true"`},
		{auto + `  match = { min_input_tokens = 0 }`, `value must be between 1 and 2000000`},
		{auto + `  match = { requested_model = " gpt-4o" }`, `must not be blank or start or end with whitespace`},
		{auto + `  match = { prompt_pattern = "(?<=x)" }`, `must be a valid RE2 regular expression`},
		{`  action = { auto = { quality_bias = 101 } }`, `value must be between 0 and 100`},
		{`  action = { failover = { targets = [{ provider = "openai", model = "gpt-4o" }] } }`, `list must contain at least 2 elements`},
		{auto + `  priority = 1000`, `value must be between 1 and 999`},
		{auto + `  on_failure = { retries = 1, wait_seconds = 2, then = "next" }`, `value must be one of`},
		{auto + `  project_id = "default"`, `must be a routeur.ai id such as proj_12`},
	}
	var steps []resource.TestStep
	for _, c := range cases {
		steps = append(steps, resource.TestStep{Config: rule(c.body), PlanOnly: true, ExpectError: regexp.MustCompile(c.err)})
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    steps,
	})
}

// The API's action error keys land on this resource's nested attributes.
func TestRoutingRuleErrorPaths(t *testing.T) {
	ctx := context.Background()
	var sresp fwresource.SchemaResponse
	NewRoutingRuleResource().Schema(ctx, fwresource.SchemaRequest{}, &sresp)
	target := func(model string) routingTargetModel {
		return routingTargetModel{Provider: types.StringValue("openai"), Model: types.StringValue(model)}
	}
	pin := &routingRuleModel{Action: &routingActionModel{Pin: &routingTargetModel{}}}
	failover := &routingRuleModel{Action: &routingActionModel{Failover: &routingFailoverModel{Targets: []routingTargetModel{target("a"), target("b")}}}}
	auto := &routingRuleModel{Action: &routingActionModel{Auto: &routingAutoModel{}}}
	for _, c := range []struct {
		m           *routingRuleModel
		field, want string
	}{
		{pin, "action.model", "action.pin.model"},
		{failover, "action.targets.1.model", "action.failover.targets[1].model"},
		{failover, "action.targets", "action.failover.targets"},
		{failover, "action.strategy", "action.failover.strategy"},
		{auto, "action.quality_bias", "action.auto.quality_bias"},
		{auto, "match.any_of.1.prompt_pattern", "match.any_of[1].prompt_pattern"},
		{auto, "on_failure.retries", "on_failure.retries"},
	} {
		p, ok := attributePath(ctx, sresp.Schema, c.field, routingErrorAliases(c.m))
		if !ok || p.String() != c.want {
			t.Errorf("%s: got %q (%v), want %q", c.field, p, ok, c.want)
		}
	}
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_routing_rule",
		schema:     "RoutingRule",
		operations: []string{"createRoutingRule", "getRoutingRule", "replaceRoutingRule", "deleteRoutingRule"},
		ignore: []string{
			"is_default",  // always false here: the Default route is routeur_default_route
			"action_kind", // implied by which of action.pin, action.auto, action.failover is set
		},
	})
}

// Targets that reference routeur_workspace_model are unknown in the first
// plan, so every validator on pin and failover targets must let unknown
// values through.
func TestAccRoutingRule_workspaceModelReferences(t *testing.T) {
	testAccEnsureCredential(t, testAccSharedCredentialProvider)
	ws := testAccSlug("ws")
	const pin, failover = "routeur_routing_rule.pin", "routeur_routing_rule.failover"
	config := fmt.Sprintf(`
resource "routeur_workspace" "test" {
  slug = %q
  name = "Model references"
}

resource "routeur_workspace_model" "gpt4o" {
  workspace_id = routeur_workspace.test.id
  model        = "openai:gpt-4o"
}

resource "routeur_workspace_model" "mini" {
  workspace_id = routeur_workspace.test.id
  model        = "openai:gpt-4o-mini"
}

resource "routeur_routing_rule" "pin" {
  workspace_id = routeur_workspace.test.id
  name         = "Pinned by reference"
  match        = { requested_model = "smart" }
  action = {
    pin = {
      provider = routeur_workspace_model.gpt4o.provider_name
      model    = routeur_workspace_model.gpt4o.model_id
    }
  }
}

resource "routeur_routing_rule" "failover" {
  workspace_id = routeur_workspace.test.id
  name         = "Failover by reference"
  match        = { requested_model = "reliable" }
  action = {
    failover = {
      targets = [
        { provider = routeur_workspace_model.gpt4o.provider_name, model = routeur_workspace_model.gpt4o.model_id },
        { provider = routeur_workspace_model.mini.provider_name, model = routeur_workspace_model.mini.model_id },
      ]
    }
  }
}
`, ws)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeTestCheckFunc(testAccCheckRoutingRuleGone(t), testAccCheckWorkspaceModelInactive(t), testAccCheckWorkspaceGone(t)),
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectUnknownValue(pin, tfjsonpath.New("action").AtMapKey("pin").AtMapKey("provider")),
					plancheck.ExpectUnknownValue(failover, tfjsonpath.New("action").AtMapKey("failover").AtMapKey("targets").AtSliceIndex(1).AtMapKey("model")),
				}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(pin, tfjsonpath.New("action").AtMapKey("pin"), knownvalue.ObjectExact(map[string]knownvalue.Check{
						"provider": knownvalue.StringExact("openai"), "model": knownvalue.StringExact("gpt-4o"),
					})),
					statecheck.ExpectKnownValue(failover, tfjsonpath.New("action").AtMapKey("failover").AtMapKey("targets").AtSliceIndex(1).AtMapKey("model"), knownvalue.StringExact("gpt-4o-mini")),
				},
			},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
}
