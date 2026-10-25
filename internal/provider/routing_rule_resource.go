package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/boolvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewRoutingRuleResource) }

var (
	_ resource.ResourceWithConfigure   = (*routingRuleResource)(nil)
	_ resource.ResourceWithImportState = (*routingRuleResource)(nil)
)

// --- API shapes ------------------------------------------------------------

// apiRoutingRule is the RoutingRule schema of the API (also the Default
// route's response).
type apiRoutingRule struct {
	ID          string             `json:"id"`
	WorkspaceID string             `json:"workspace_id"`
	ProjectID   *string            `json:"project_id"`
	Slug        string             `json:"slug"`
	Name        string             `json:"name"`
	Priority    int64              `json:"priority"`
	Enabled     bool               `json:"enabled"`
	IsDefault   bool               `json:"is_default"`
	ActionKind  string             `json:"action_kind"`
	Match       apiRoutingMatch    `json:"match"`
	Action      apiRoutingAction   `json:"action"`
	OnFailure   *apiRoutingFailure `json:"on_failure"`
	CreatedAt   string             `json:"created_at"`
	UpdatedAt   string             `json:"updated_at"`
}

// apiRoutingConditions are the conditions of one group (AND-ed).
type apiRoutingConditions struct {
	RequestedModel *string `json:"requested_model,omitempty"`
	PromptContains *string `json:"prompt_contains,omitempty"`
	PromptPattern  *string `json:"prompt_pattern,omitempty"`
	MinInputTokens *int64  `json:"min_input_tokens,omitempty"`
	ContainsImage  *bool   `json:"contains_image,omitempty"`
}

// apiRoutingMatch is `{}`, one group's conditions at the top level, or
// `{"any_of": [...]}` — never both.
type apiRoutingMatch struct {
	apiRoutingConditions
	AnyOf []apiRoutingConditions `json:"any_of,omitempty"`
}

// apiRoutingAction is the union of the three action shapes; action_kind
// says which fields are set.
type apiRoutingAction struct {
	Provider    *string            `json:"provider,omitempty"`
	Model       *string            `json:"model,omitempty"`
	QualityBias *int64             `json:"quality_bias,omitempty"`
	Strategy    *string            `json:"strategy,omitempty"`
	Targets     []apiRoutingTarget `json:"targets,omitempty"`
}

type apiRoutingTarget struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type apiRoutingFailure struct {
	Retries     int64  `json:"retries"`
	WaitSeconds int64  `json:"wait_seconds"`
	Then        string `json:"then"`
}

// Action kinds (action_kind in the API).
const (
	routingKindPin      = "pin"
	routingKindAuto     = "auto"
	routingKindFailover = "failover"
)

// --- Terraform models --------------------------------------------------------

type routingRuleModel struct {
	ID          types.String         `tfsdk:"id"`
	WorkspaceID types.String         `tfsdk:"workspace_id"`
	ProjectID   types.String         `tfsdk:"project_id"`
	Slug        types.String         `tfsdk:"slug"`
	Name        types.String         `tfsdk:"name"`
	Priority    types.Int64          `tfsdk:"priority"`
	Enabled     types.Bool           `tfsdk:"enabled"`
	Match       *routingMatchModel   `tfsdk:"match"`
	Action      *routingActionModel  `tfsdk:"action"`
	OnFailure   *routingFailureModel `tfsdk:"on_failure"`
	CreatedAt   timetypes.RFC3339    `tfsdk:"created_at"`
	UpdatedAt   timetypes.RFC3339    `tfsdk:"updated_at"`
}

type routingConditionsModel struct {
	RequestedModel types.String `tfsdk:"requested_model"`
	PromptContains types.String `tfsdk:"prompt_contains"`
	PromptPattern  types.String `tfsdk:"prompt_pattern"`
	MinInputTokens types.Int64  `tfsdk:"min_input_tokens"`
	ContainsImage  types.Bool   `tfsdk:"contains_image"`
}

type routingMatchModel struct {
	RequestedModel types.String             `tfsdk:"requested_model"`
	PromptContains types.String             `tfsdk:"prompt_contains"`
	PromptPattern  types.String             `tfsdk:"prompt_pattern"`
	MinInputTokens types.Int64              `tfsdk:"min_input_tokens"`
	ContainsImage  types.Bool               `tfsdk:"contains_image"`
	AnyOf          []routingConditionsModel `tfsdk:"any_of"`
}

type routingActionModel struct {
	Pin      *routingTargetModel   `tfsdk:"pin"`
	Auto     *routingAutoModel     `tfsdk:"auto"`
	Failover *routingFailoverModel `tfsdk:"failover"`
}

type routingTargetModel struct {
	Provider types.String `tfsdk:"provider"`
	Model    types.String `tfsdk:"model"`
}

type routingAutoModel struct {
	QualityBias types.Int64 `tfsdk:"quality_bias"`
}

type routingFailoverModel struct {
	Strategy types.String         `tfsdk:"strategy"`
	Targets  []routingTargetModel `tfsdk:"targets"`
}

type routingFailureModel struct {
	Retries     types.Int64  `tfsdk:"retries"`
	WaitSeconds types.Int64  `tfsdk:"wait_seconds"`
	Then        types.String `tfsdk:"then"`
}

func (m *routingRuleModel) fromAPI(r apiRoutingRule) {
	m.ID = types.StringValue(r.ID)
	m.WorkspaceID = types.StringValue(r.WorkspaceID)
	m.ProjectID = types.StringPointerValue(r.ProjectID)
	m.Slug = types.StringValue(r.Slug)
	m.Name = types.StringValue(r.Name)
	m.Priority = types.Int64Value(r.Priority)
	m.Enabled = types.BoolValue(r.Enabled)
	m.Match = matchFromAPI(r.Match)
	m.Action = actionFromAPI(r.ActionKind, r.Action)
	m.OnFailure = nil
	if r.OnFailure != nil {
		m.OnFailure = &routingFailureModel{
			Retries:     types.Int64Value(r.OnFailure.Retries),
			WaitSeconds: types.Int64Value(r.OnFailure.WaitSeconds),
			Then:        types.StringValue(r.OnFailure.Then),
		}
	}
	m.CreatedAt = rfc3339(r.CreatedAt)
	m.UpdatedAt = rfc3339(r.UpdatedAt)
}

func conditionsFromAPI(c apiRoutingConditions) routingConditionsModel {
	return routingConditionsModel{
		RequestedModel: types.StringPointerValue(c.RequestedModel),
		PromptContains: types.StringPointerValue(c.PromptContains),
		PromptPattern:  types.StringPointerValue(c.PromptPattern),
		MinInputTokens: types.Int64PointerValue(c.MinInputTokens),
		ContainsImage:  types.BoolPointerValue(c.ContainsImage),
	}
}

// matchFromAPI maps `{}` (every request) to a null match, so leaving match
// out of the configuration and reading the rule back agree.
func matchFromAPI(a apiRoutingMatch) *routingMatchModel {
	top := conditionsFromAPI(a.apiRoutingConditions)
	m := &routingMatchModel{
		RequestedModel: top.RequestedModel,
		PromptContains: top.PromptContains,
		PromptPattern:  top.PromptPattern,
		MinInputTokens: top.MinInputTokens,
		ContainsImage:  top.ContainsImage,
	}
	for _, g := range a.AnyOf {
		m.AnyOf = append(m.AnyOf, conditionsFromAPI(g))
	}
	if len(m.AnyOf) == 0 && top.RequestedModel.IsNull() && top.PromptContains.IsNull() && top.PromptPattern.IsNull() &&
		top.MinInputTokens.IsNull() && top.ContainsImage.IsNull() {
		return nil
	}
	return m
}

// actionFromAPI sets the one nested object action_kind names. An unknown
// kind (the enum may grow) reads as an action with nothing set rather than
// failing, so the next plan shows the difference.
func actionFromAPI(kind string, a apiRoutingAction) *routingActionModel {
	out := &routingActionModel{}
	switch kind {
	case routingKindPin:
		out.Pin = &routingTargetModel{Provider: types.StringPointerValue(a.Provider), Model: types.StringPointerValue(a.Model)}
	case routingKindAuto:
		out.Auto = &routingAutoModel{QualityBias: types.Int64PointerValue(a.QualityBias)}
	case routingKindFailover:
		f := &routingFailoverModel{Strategy: types.StringPointerValue(a.Strategy), Targets: []routingTargetModel{}}
		for _, t := range a.Targets {
			f.Targets = append(f.Targets, routingTargetModel{Provider: types.StringValue(t.Provider), Model: types.StringValue(t.Model)})
		}
		out.Failover = f
	}
	return out
}

// --- request bodies ----------------------------------------------------------

func conditionsBody(c routingConditionsModel) map[string]any {
	b := map[string]any{}
	if !c.RequestedModel.IsNull() {
		b["requested_model"] = c.RequestedModel.ValueString()
	}
	if !c.PromptContains.IsNull() {
		b["prompt_contains"] = c.PromptContains.ValueString()
	}
	if !c.PromptPattern.IsNull() {
		b["prompt_pattern"] = c.PromptPattern.ValueString()
	}
	if !c.MinInputTokens.IsNull() {
		b["min_input_tokens"] = c.MinInputTokens.ValueInt64()
	}
	if !c.ContainsImage.IsNull() {
		b["contains_image"] = c.ContainsImage.ValueBool()
	}
	return b
}

func (m *routingMatchModel) body() map[string]any {
	if m == nil {
		return map[string]any{}
	}
	if len(m.AnyOf) > 0 {
		groups := make([]any, 0, len(m.AnyOf))
		for _, g := range m.AnyOf {
			groups = append(groups, conditionsBody(g))
		}
		return map[string]any{"any_of": groups}
	}
	return conditionsBody(routingConditionsModel{
		RequestedModel: m.RequestedModel,
		PromptContains: m.PromptContains,
		PromptPattern:  m.PromptPattern,
		MinInputTokens: m.MinInputTokens,
		ContainsImage:  m.ContainsImage,
	})
}

// body returns action_kind and the action object.
func (a *routingActionModel) body() (string, map[string]any) {
	switch {
	case a == nil:
		return "", nil
	case a.Pin != nil:
		return routingKindPin, map[string]any{"provider": a.Pin.Provider.ValueString(), "model": a.Pin.Model.ValueString()}
	case a.Auto != nil:
		return routingKindAuto, map[string]any{"quality_bias": a.Auto.QualityBias.ValueInt64()}
	case a.Failover != nil:
		targets := make([]any, 0, len(a.Failover.Targets))
		for _, t := range a.Failover.Targets {
			targets = append(targets, map[string]any{"provider": t.Provider.ValueString(), "model": t.Model.ValueString()})
		}
		return routingKindFailover, map[string]any{"strategy": a.Failover.Strategy.ValueString(), "targets": targets}
	}
	return "", nil
}

// body is the full desired rule (POST and PUT). name and priority are sent
// only when known: absent keeps them on PUT and lets the server choose on
// create.
func (m *routingRuleModel) body() map[string]any {
	kind, action := m.Action.body()
	b := map[string]any{
		"project_id":  nullableString(m.ProjectID),
		"enabled":     m.Enabled.ValueBool(),
		"action_kind": kind,
		"match":       m.Match.body(),
		"action":      action,
		"on_failure":  nil,
	}
	if !m.Name.IsNull() && !m.Name.IsUnknown() {
		b["name"] = m.Name.ValueString()
	}
	if !m.Priority.IsNull() && !m.Priority.IsUnknown() {
		b["priority"] = m.Priority.ValueInt64()
	}
	if f := m.OnFailure; f != nil {
		b["on_failure"] = map[string]any{
			"retries":      f.Retries.ValueInt64(),
			"wait_seconds": f.WaitSeconds.ValueInt64(),
			"then":         f.Then.ValueString(),
		}
	}
	return b
}

// routingErrorAliases maps the API's action error keys (`action.model`,
// `action.targets.1.model`) onto this resource's nested attributes.
func routingErrorAliases(m *routingRuleModel) map[string]string {
	kind, _ := m.Action.body()
	a := map[string]string{}
	switch kind {
	case routingKindPin:
		a["action.provider"] = "action.pin.provider"
		a["action.model"] = "action.pin.model"
	case routingKindAuto:
		a["action.quality_bias"] = "action.auto.quality_bias"
	case routingKindFailover:
		a["action.strategy"] = "action.failover.strategy"
		a["action.targets"] = "action.failover.targets"
		for i := range m.Action.Failover.Targets {
			for _, f := range []string{"", ".provider", ".model"} {
				a[fmt.Sprintf("action.targets.%d%s", i, f)] = fmt.Sprintf("action.failover.targets.%d%s", i, f)
			}
		}
	}
	return a
}

// --- schema ------------------------------------------------------------------

const (
	routingRuleDescription = "A routing rule in a workspace: when a gateway request matches, routeur.ai pins it to a model, " +
		"auto-routes it with a quality bias, or fails over across several models. Rules are evaluated in ascending " +
		"`priority`; the first enabled match wins, and the workspace's Default route (`routeur_default_route`) " +
		"catches the rest. Every model a rule names must be activated in the workspace: take `provider` and `model` from " +
		"`routeur_workspace_model`'s `provider_name` and `model_id` (rather than writing them out and adding `depends_on`), " +
		"so Terraform activates the model before the rule needs it and keeps it until the rule is gone."
	routingRuleIDDescription        = "Public id of the routing rule (`route_…`)."
	routingRuleProjectIDDescription = "Limit the rule to one project in the workspace (`proj_…`). Leave it out to apply the rule " +
		"to every project in the workspace. A project must be enabled when it is newly targeted."
	routingRuleSlugDescription = "Server-assigned and immutable; traces record a match as `rule:<slug>`."
	routingRuleNameDescription = "Display name, unique in the workspace, 1–60 characters without leading or trailing " +
		"whitespace. If left out, routeur.ai generates one from the rule when it is created; removing the argument later " +
		"keeps the current name."
	routingRulePriorityDescription = "Evaluation order, 1–999: lower runs first (the Default route owns 1000). If left out, the " +
		"rule is placed after the workspace's existing rules when it is created; removing the argument later keeps the " +
		"current priority. Priorities need not be unique: rules with the same priority run in the order they were created."
	routingRuleEnabledDescription = "Whether the rule is evaluated. Defaults to `true`; removing the argument enables the rule again."
	routingRuleMatchDescription   = "Which requests the rule applies to. Leave it out to match every request. Set conditions " +
		"directly in `match` (all must hold), or list two to ten condition groups in `any_of` (at least one group must " +
		"hold) — not both."
	routingRuleAnyOfDescription = "Two to ten condition groups, OR-ed; the conditions inside a group are AND-ed. For a single " +
		"group, set its conditions directly in `match` instead."
	routingRuleActionDescription    = "What the rule does with a matching request. Set exactly one of `pin`, `auto` or `failover`."
	routingRuleOnFailureDescription = "Failure policy: retry the same target, then move on or return the error. Leave it out for " +
		"the default behaviour."
)

// conditionAttributes are the conditions of a group, shared by `match`
// and each `any_of` group.
func conditionAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"requested_model": schema.StringAttribute{
			MarkdownDescription: "The model the caller asked for equals this value, 1–80 characters.",
			Optional:            true,
			Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(80), noSurroundingWhitespace()},
		},
		"prompt_contains": schema.StringAttribute{
			MarkdownDescription: "The prompt contains this text, 1–200 characters.",
			Optional:            true,
			Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(200), noSurroundingWhitespace()},
		},
		"prompt_pattern": schema.StringAttribute{
			MarkdownDescription: "The prompt matches this RE2 regular expression, 1–500 characters. Stored verbatim.",
			Optional:            true,
			Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 500), re2Pattern()},
		},
		"min_input_tokens": schema.Int64Attribute{
			MarkdownDescription: "The request has at least this many input tokens, 1–2,000,000.",
			Optional:            true,
			Validators:          []validator.Int64{int64validator.Between(1, 2_000_000)},
		},
		"contains_image": schema.BoolAttribute{
			MarkdownDescription: "The request contains an image. Only `true` is a condition; leave it out otherwise.",
			Optional:            true,
			Validators:          []validator.Bool{boolvalidator.Equals(true)},
		},
	}
}

var conditionNames = []string{"requested_model", "prompt_contains", "prompt_pattern", "min_input_tokens", "contains_image"}

// routingTargetAttributes is a {provider, model} pair.
func routingTargetAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"provider": schema.StringAttribute{
			MarkdownDescription: "Upstream provider slug: `openai`, `anthropic`, `google`, `deepseek` or `mistral` (new providers may be added). Typically `routeur_workspace_model.<name>.provider_name`.",
			Required:            true,
			Validators:          []validator.String{stringvalidator.UTF8LengthAtLeast(1)},
		},
		"model": schema.StringAttribute{
			MarkdownDescription: "Model id at that provider, e.g. `gpt-4o`. It must be activated in the workspace: typically `routeur_workspace_model.<name>.model_id`.",
			Required:            true,
			Validators:          []validator.String{stringvalidator.UTF8LengthAtLeast(1)},
		},
	}
}

type routingRuleResource struct{ resourceBase }

// NewRoutingRuleResource is the routeur_routing_rule resource.
func NewRoutingRuleResource() resource.Resource {
	return &routingRuleResource{resourceBase{name: "routing_rule"}}
}

func (r *routingRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	matchAttrs := conditionAttributes()
	var topLevel []path.Expression
	for _, n := range conditionNames {
		topLevel = append(topLevel, path.MatchRelative().AtParent().AtName(n))
	}
	matchAttrs["any_of"] = schema.ListNestedAttribute{
		MarkdownDescription: routingRuleAnyOfDescription,
		Optional:            true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: conditionAttributes(),
			Validators: []validator.Object{atLeastOneAttribute("a condition group needs at least one condition")},
		},
		Validators: []validator.List{
			listvalidator.SizeBetween(2, 10),
			listvalidator.ConflictsWith(topLevel...),
		},
	}

	sibling := func(n string) path.Expression { return path.MatchRelative().AtParent().AtName(n) }

	resp.Schema = schema.Schema{
		MarkdownDescription: routingRuleDescription,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute(routingRuleIDDescription),
			"workspace_id": workspaceIDAttribute(),
			"project_id": schema.StringAttribute{
				MarkdownDescription: routingRuleProjectIDDescription,
				Optional:            true,
				Validators:          []validator.String{publicID("proj")},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: routingRuleSlugDescription,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: routingRuleNameDescription,
				Optional:            true,
				Computed:            true,
				Validators:          name(60),
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"priority": schema.Int64Attribute{
				MarkdownDescription: routingRulePriorityDescription,
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Int64{int64validator.Between(1, 999)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: routingRuleEnabledDescription,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"match": schema.SingleNestedAttribute{
				MarkdownDescription: routingRuleMatchDescription,
				Optional:            true,
				Attributes:          matchAttrs,
				Validators:          []validator.Object{atLeastOneAttribute("set at least one condition or any_of, or leave match out to match every request")},
			},
			"action": schema.SingleNestedAttribute{
				MarkdownDescription: routingRuleActionDescription,
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"pin": schema.SingleNestedAttribute{
						MarkdownDescription: "Send matching requests to one model.",
						Optional:            true,
						Attributes:          routingTargetAttributes(),
						Validators: []validator.Object{
							objectvalidator.ExactlyOneOf(sibling("auto"), sibling("failover")),
						},
					},
					"auto": schema.SingleNestedAttribute{
						MarkdownDescription: "Let the auto-router pick the cheapest activated model that clears the quality bar. `auto = {}` uses the neutral bias.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"quality_bias": schema.Int64Attribute{
								MarkdownDescription: "0 (cheapest) to 100 (smartest). Defaults to `50`, which is neutral.",
								Optional:            true,
								Computed:            true,
								Default:             int64default.StaticInt64(50),
								Validators:          []validator.Int64{int64validator.Between(0, 100)},
							},
						},
					},
					"failover": schema.SingleNestedAttribute{
						MarkdownDescription: "Try several models, one after another or all at once.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"strategy": schema.StringAttribute{
								MarkdownDescription: "`sequential` (one at a time, in order) or `race` (all at once, first answer wins). Defaults to `sequential`.",
								Optional:            true,
								Computed:            true,
								Default:             stringdefault.StaticString("sequential"),
								Validators:          []validator.String{stringvalidator.OneOf("sequential", "race")},
							},
							"targets": schema.ListNestedAttribute{
								MarkdownDescription: "Two to five models, in order.",
								Required:            true,
								NestedObject:        schema.NestedAttributeObject{Attributes: routingTargetAttributes()},
								Validators:          []validator.List{listvalidator.SizeBetween(2, 5)},
							},
						},
					},
				},
			},
			"on_failure": schema.SingleNestedAttribute{
				MarkdownDescription: routingRuleOnFailureDescription,
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"retries": schema.Int64Attribute{
						MarkdownDescription: "How many more times to try the same target, 0–5.",
						Required:            true,
						Validators:          []validator.Int64{int64validator.Between(0, 5)},
					},
					"wait_seconds": schema.Int64Attribute{
						MarkdownDescription: "Pause between retries: `0`, `1`, `5` or `30` seconds.",
						Required:            true,
						Validators:          []validator.Int64{int64validator.OneOf(0, 1, 5, 30)},
					},
					"then": schema.StringAttribute{
						MarkdownDescription: "After the retries: `next` (the next failover target, or the next-best auto-routed model) or `error` (return the upstream error).",
						Required:            true,
						Validators:          []validator.String{stringvalidator.OneOf("next", "error")},
					},
				},
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

func (r *routingRuleResource) rulePath(m routingRuleModel) string {
	return r.workspacePath(m.WorkspaceID.ValueString(), "routing-rules", m.ID.ValueString())
}

func (r *routingRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan routingRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := client.Send[apiRoutingRule](ctx, r.client, http.MethodPost, r.workspacePath(plan.WorkspaceID.ValueString(), "routing-rules"), plan.body())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not create routing rule", err, routingErrorAliases(&plan))
		return
	}
	plan.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *routingRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state routingRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := client.Get[apiRoutingRule](ctx, r.client, r.rulePath(state))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read routing rule", err)
		return
	}
	if rule.IsDefault {
		resp.Diagnostics.AddError("Not a custom routing rule",
			fmt.Sprintf("%s is the Default route of workspace %s. Manage it with the routeur_default_route resource (import id %q).", rule.ID, rule.WorkspaceID, rule.WorkspaceID))
		return
	}
	state.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *routingRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state routingRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// PUT: the full desired rule; absent match / project_id / on_failure clear them.
	rule, err := client.Send[apiRoutingRule](ctx, r.client, http.MethodPut, r.rulePath(state), plan.body())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update routing rule", err, routingErrorAliases(&plan))
		return
	}
	plan.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *routingRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state routingRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Do(ctx, http.MethodDelete, r.rulePath(state), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not delete routing rule "+state.Name.ValueString(), err)
	}
}

// ImportState takes `<workspace_id>/<id>`, e.g. `ws_3/route_12`.
func (r *routingRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req, resp, "route")
}

// --- validators --------------------------------------------------------------

// atLeastOneAttribute refuses a known object whose attributes are all null:
// the API would store it as "no conditions", which reads back differently.
func atLeastOneAttribute(message string) validator.Object {
	return atLeastOneAttributeValidator{message}
}

type atLeastOneAttributeValidator struct{ message string }

func (v atLeastOneAttributeValidator) Description(context.Context) string { return v.message }

func (v atLeastOneAttributeValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v atLeastOneAttributeValidator) ValidateObject(_ context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for _, a := range req.ConfigValue.Attributes() {
		if !a.IsNull() {
			return
		}
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Empty object", fmt.Sprintf("%s: %s.", req.Path, v.message))
}
