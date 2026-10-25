package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewBudgetResource) }

var (
	_ resource.ResourceWithConfigure      = (*budgetResource)(nil)
	_ resource.ResourceWithImportState    = (*budgetResource)(nil)
	_ resource.ResourceWithValidateConfig = (*budgetResource)(nil)
)

// apiBudget is the Budget schema of the API.
type apiBudget struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	ScopeType         string  `json:"scope_type"`
	ScopeID           *string `json:"scope_id"`
	Period            string  `json:"period"`
	AmountCents       int64   `json:"amount_cents"`
	Currency          string  `json:"currency"`
	AlertThresholdPct int64   `json:"alert_threshold_pct"`
	HardBlock         bool    `json:"hard_block"`
	Enabled           bool    `json:"enabled"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
}

type budgetModel struct {
	ID                types.String      `tfsdk:"id"`
	Name              types.String      `tfsdk:"name"`
	ScopeType         types.String      `tfsdk:"scope_type"`
	ScopeID           types.String      `tfsdk:"scope_id"`
	Period            types.String      `tfsdk:"period"`
	AmountCents       types.Int64       `tfsdk:"amount_cents"`
	Currency          types.String      `tfsdk:"currency"`
	AlertThresholdPct types.Int64       `tfsdk:"alert_threshold_pct"`
	HardBlock         types.Bool        `tfsdk:"hard_block"`
	Enabled           types.Bool        `tfsdk:"enabled"`
	CreatedAt         timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt         timetypes.RFC3339 `tfsdk:"updated_at"`
}

func (m *budgetModel) fromAPI(b apiBudget) {
	m.ID = types.StringValue(b.ID)
	m.Name = types.StringValue(b.Name)
	m.ScopeType = types.StringValue(b.ScopeType)
	m.ScopeID = types.StringPointerValue(b.ScopeID)
	m.Period = types.StringValue(b.Period)
	m.AmountCents = types.Int64Value(b.AmountCents)
	m.Currency = types.StringValue(b.Currency)
	m.AlertThresholdPct = types.Int64Value(b.AlertThresholdPct)
	m.HardBlock = types.BoolValue(b.HardBlock)
	m.Enabled = types.BoolValue(b.Enabled)
	m.CreatedAt = rfc3339(b.CreatedAt)
	m.UpdatedAt = rfc3339(b.UpdatedAt)
}

// publicIDAnyPattern matches a workspace, project or API key id.
var publicIDAnyPattern = regexp.MustCompile(`^(ws|proj|key)_[1-9][0-9]*$`)

// budgetScopePrefix is the public-id prefix scope_id must carry per scope_type.
var budgetScopePrefix = map[string]string{"workspace": "ws", "project": "proj", "api_key": "key"}

const (
	budgetDescription                = "A spend budget of the organisation, watching the whole organisation or one workspace, project or API key. It alerts by email at a threshold and can optionally make the gateway refuse requests once the cap is exceeded. Requires a routeur.ai plan with budgets to create or change."
	budgetIDDescription              = "Public id of the budget (`bud_…`)."
	budgetNameDescription            = "Display name, 1–80 characters, without leading or trailing whitespace."
	budgetScopeTypeDescription       = "What the budget watches: `organisation`, `workspace`, `project` or `api_key`. `api_key` budgets alert but cannot be hard-blocked at the gateway. Can be changed in place."
	budgetScopeIDDescription         = "Id of the workspace (`ws_…`), project (`proj_…`) or API key (`key_…`) being watched, in this organisation. Required for every `scope_type` except `organisation`, where it must be omitted."
	budgetPeriodDescription          = "`monthly` (calendar month, UTC) or `weekly` (Monday to Sunday, UTC). Defaults to `monthly`."
	budgetAmountDescription          = "The cap in US cents, 1 to 100,000,000 (for example `50000` is $500.00). Never a decimal."
	budgetCurrencyDescription        = "Always `USD` today."
	budgetThresholdDescription       = "Percentage of the cap, 1 to 100, at which the warning email is sent. Defaults to `80`."
	budgetHardBlockDescription       = "When `true` the gateway refuses requests once the cap is exceeded; otherwise the budget only alerts. Defaults to `false`. Applied on the next budgets evaluation pass, not instantly."
	budgetEnabledDescription         = "Whether the budget is evaluated and enforced. Defaults to `true`; removing the argument enables the budget again."
	budgetMaxAmountCents       int64 = 100_000_000
)

type budgetResource struct{ resourceBase }

// NewBudgetResource is the routeur_budget resource.
func NewBudgetResource() resource.Resource {
	return &budgetResource{resourceBase{name: "budget"}}
}

func (r *budgetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: budgetDescription,
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(budgetIDDescription),
			"name": schema.StringAttribute{
				MarkdownDescription: budgetNameDescription,
				Required:            true,
				Validators:          name(80),
			},
			"scope_type": schema.StringAttribute{
				MarkdownDescription: budgetScopeTypeDescription,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("organisation", "workspace", "project", "api_key")},
			},
			"scope_id": schema.StringAttribute{
				MarkdownDescription: budgetScopeIDDescription,
				Optional:            true,
				Validators: []validator.String{stringvalidator.RegexMatches(
					publicIDAnyPattern, "must be a routeur.ai id such as ws_12, proj_12 or key_12",
				)},
			},
			"period": schema.StringAttribute{
				MarkdownDescription: budgetPeriodDescription,
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("monthly"),
				Validators:          []validator.String{stringvalidator.OneOf("monthly", "weekly")},
			},
			"amount_cents": schema.Int64Attribute{
				MarkdownDescription: budgetAmountDescription,
				Required:            true,
				Validators:          []validator.Int64{int64validator.Between(1, budgetMaxAmountCents)},
			},
			"currency": schema.StringAttribute{
				MarkdownDescription: budgetCurrencyDescription,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"alert_threshold_pct": schema.Int64Attribute{
				MarkdownDescription: budgetThresholdDescription,
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(80),
				Validators:          []validator.Int64{int64validator.Between(1, 100)},
			},
			"hard_block": schema.BoolAttribute{
				MarkdownDescription: budgetHardBlockDescription,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: budgetEnabledDescription,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

// ValidateConfig ties scope_id to scope_type at plan time.
func (r *budgetResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var scopeType, scopeID types.String
	var hardBlock types.Bool
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("scope_type"), &scopeType)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("scope_id"), &scopeID)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("hard_block"), &hardBlock)...)
	if resp.Diagnostics.HasError() || scopeType.IsUnknown() || scopeType.IsNull() {
		return
	}
	if scopeType.ValueString() == "api_key" && hardBlock.ValueBool() {
		// The API accepts the flag and stores it, but the gateway cannot enforce it.
		resp.Diagnostics.AddAttributeWarning(path.Root("hard_block"), "hard_block has no effect on an API key budget",
			"API key budgets alert but cannot be hard-blocked at the gateway; the setting is stored but never enforced.")
	}
	prefix, scoped := budgetScopePrefix[scopeType.ValueString()]
	switch {
	case !scoped && !scopeID.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("scope_id"), "Unexpected scope_id", `scope_id must be omitted when scope_type is "organisation".`)
	case scoped && scopeID.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("scope_id"), "Missing scope_id", `scope_id is required when scope_type is "`+scopeType.ValueString()+`".`)
	case scoped && !scopeID.IsUnknown() && !publicIDPattern(prefix).MatchString(scopeID.ValueString()):
		resp.Diagnostics.AddAttributeError(path.Root("scope_id"), "Invalid scope_id", `scope_id must be a `+prefix+`_… id when scope_type is "`+scopeType.ValueString()+`".`)
	}
}

func (r *budgetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan budgetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := map[string]any{
		"name":                plan.Name.ValueString(),
		"scope_type":          plan.ScopeType.ValueString(),
		"scope_id":            nullableString(plan.ScopeID),
		"period":              plan.Period.ValueString(),
		"amount_cents":        plan.AmountCents.ValueInt64(),
		"alert_threshold_pct": plan.AlertThresholdPct.ValueInt64(),
		"hard_block":          plan.HardBlock.ValueBool(),
		"enabled":             plan.Enabled.ValueBool(),
	}
	b, err := client.Send[apiBudget](ctx, r.client, http.MethodPost, r.orgPath("budgets"), body)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not create budget", err)
		return
	}
	plan.fromAPI(b)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *budgetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state budgetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	b, err := client.Get[apiBudget](ctx, r.client, r.orgPath("budgets", state.ID.ValueString()))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read budget", err)
		return
	}
	state.fromAPI(b)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *budgetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state budgetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// PATCH: send only what changed. A scope change always sends both
	// halves (the API never keeps the old id for a new type); scope_id null
	// is the organisation scope.
	body := map[string]any{}
	if !plan.Name.Equal(state.Name) {
		body["name"] = plan.Name.ValueString()
	}
	if !plan.ScopeType.Equal(state.ScopeType) || !plan.ScopeID.Equal(state.ScopeID) {
		body["scope_type"] = plan.ScopeType.ValueString()
		body["scope_id"] = nullableString(plan.ScopeID)
	}
	if !plan.Period.Equal(state.Period) {
		body["period"] = plan.Period.ValueString()
	}
	if !plan.AmountCents.Equal(state.AmountCents) {
		body["amount_cents"] = plan.AmountCents.ValueInt64()
	}
	if !plan.AlertThresholdPct.Equal(state.AlertThresholdPct) {
		body["alert_threshold_pct"] = plan.AlertThresholdPct.ValueInt64()
	}
	if !plan.HardBlock.Equal(state.HardBlock) {
		body["hard_block"] = plan.HardBlock.ValueBool()
	}
	if !plan.Enabled.Equal(state.Enabled) {
		body["enabled"] = plan.Enabled.ValueBool()
	}

	b, err := client.Send[apiBudget](ctx, r.client, http.MethodPatch, r.orgPath("budgets", state.ID.ValueString()), body)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update budget", err)
		return
	}
	plan.fromAPI(b)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *budgetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state budgetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Do(ctx, http.MethodDelete, r.orgPath("budgets", state.ID.ValueString()), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not delete budget "+state.Name.ValueString(), err)
	}
}

// ImportState takes the budget id, e.g. `bud_3`.
func (r *budgetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !publicIDPattern("bud").MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("expected a budget id such as bud_3, got %q.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
