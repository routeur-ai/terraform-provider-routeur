package provider

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewClassifierRuleResource) }

var (
	_ resource.ResourceWithConfigure   = (*classifierRuleResource)(nil)
	_ resource.ResourceWithImportState = (*classifierRuleResource)(nil)
)

// apiClassifierRule is the ClassifierRule schema of the API.
type apiClassifierRule struct {
	ID          string      `json:"id"`
	WorkspaceID string      `json:"workspace_id"`
	ProjectID   *string     `json:"project_id"`
	Name        string      `json:"name"`
	Slug        string      `json:"slug"`
	Axis        string      `json:"axis"`
	MinScore    json.Number `json:"min_score"`
	Action      string      `json:"action"`
	Enabled     bool        `json:"enabled"`
	CreatedAt   string      `json:"created_at"`
	UpdatedAt   string      `json:"updated_at"`
}

type classifierRuleModel struct {
	ID          types.String      `tfsdk:"id"`
	WorkspaceID types.String      `tfsdk:"workspace_id"`
	ProjectID   types.String      `tfsdk:"project_id"`
	Name        types.String      `tfsdk:"name"`
	Slug        types.String      `tfsdk:"slug"`
	Axis        types.String      `tfsdk:"axis"`
	MinScore    types.Float64     `tfsdk:"min_score"`
	Action      types.String      `tfsdk:"action"`
	Enabled     types.Bool        `tfsdk:"enabled"`
	CreatedAt   timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt   timetypes.RFC3339 `tfsdk:"updated_at"`
}

func (m *classifierRuleModel) fromAPI(c apiClassifierRule) {
	m.ID = types.StringValue(c.ID)
	m.WorkspaceID = types.StringValue(c.WorkspaceID)
	m.ProjectID = types.StringPointerValue(c.ProjectID)
	m.Name = types.StringValue(c.Name)
	m.Slug = types.StringValue(c.Slug)
	m.Axis = types.StringValue(c.Axis)
	m.MinScore = decimalFloat64(c.MinScore)
	m.Action = types.StringValue(c.Action)
	m.Enabled = types.BoolValue(c.Enabled)
	m.CreatedAt = rfc3339(c.CreatedAt)
	m.UpdatedAt = rfc3339(c.UpdatedAt)
}

// body is the full desired rule, for POST and PUT alike: PUT replaces, so
// project_id (null = every project) and enabled are always sent.
func (m *classifierRuleModel) body() map[string]any {
	return map[string]any{
		"name":       m.Name.ValueString(),
		"slug":       m.Slug.ValueString(),
		"axis":       m.Axis.ValueString(),
		"min_score":  m.MinScore.ValueFloat64(),
		"action":     m.Action.ValueString(),
		"project_id": nullableString(m.ProjectID),
		"enabled":    m.Enabled.ValueBool(),
	}
}

const (
	classifierDescription = "A classifier rule in a workspace. It fires when the auto-router's classifier score on one axis (toxicity, coding, …) reaches `min_score`, and then logs, flags or blocks the prompt."
	classifierIDDesc      = "Public id of the rule (`clf_…`)."
	classifierProjectDesc = "Scope the rule to one project in this workspace (`proj_…`). Omit it to apply to every project in the workspace. Changing it updates the rule in place."
	classifierNameDesc    = "Unique in the workspace, 1–80 characters, without leading or trailing whitespace."
	classifierSlugDesc    = "Machine identifier, unique in the workspace: lowercase letters and digits joined by single underscores, at most 64 characters. It can be changed in place."
	classifierAxisDesc    = "The classifier score the rule reads: `toxicity`, `hate`, `violence`, `weapons`, `coding`, `long_reasoning`, `large_context`, `structured_output` or `creative`."
	classifierScoreDesc   = "Fires when the score on `axis` is greater than or equal to this value, between 0 and 1."
	classifierActionDesc  = "`log` or `flag` only report; `block` rejects the prompt with HTTP 403."
	classifierEnabledDesc = "Whether the rule is evaluated. Defaults to `true`; removing the argument enables the rule again."
)

type classifierRuleResource struct{ resourceBase }

// NewClassifierRuleResource is the routeur_classifier_rule resource.
func NewClassifierRuleResource() resource.Resource {
	return &classifierRuleResource{resourceBase{name: "classifier_rule"}}
}

func (r *classifierRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: classifierDescription,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute(classifierIDDesc),
			"workspace_id": workspaceIDAttribute(),
			"project_id": schema.StringAttribute{
				MarkdownDescription: classifierProjectDesc,
				Optional:            true,
				Validators:          []validator.String{publicID("proj")},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: classifierNameDesc,
				Required:            true,
				Validators:          name(80),
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: classifierSlugDesc,
				Required:            true,
				Validators:          underscoreSlug(),
			},
			"axis": schema.StringAttribute{
				MarkdownDescription: classifierAxisDesc,
				Required:            true,
				Validators: []validator.String{stringvalidator.OneOf(
					"toxicity", "hate", "violence", "weapons", "coding",
					"long_reasoning", "large_context", "structured_output", "creative")},
			},
			"min_score": schema.Float64Attribute{
				MarkdownDescription: classifierScoreDesc,
				Required:            true,
				Validators:          []validator.Float64{float64validator.Between(0, 1)},
			},
			"action": schema.StringAttribute{
				MarkdownDescription: classifierActionDesc,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("log", "flag", "block")},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: classifierEnabledDesc,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

func (r *classifierRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan classifierRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.Send[apiClassifierRule](ctx, r.client, http.MethodPost, r.workspacePath(plan.WorkspaceID.ValueString(), "classifier-rules"), plan.body())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not create classifier rule", err)
		return
	}
	plan.fromAPI(c)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *classifierRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state classifierRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.Get[apiClassifierRule](ctx, r.client, r.workspacePath(state.WorkspaceID.ValueString(), "classifier-rules", state.ID.ValueString()))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read classifier rule", err)
		return
	}
	state.fromAPI(c)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *classifierRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state classifierRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.Send[apiClassifierRule](ctx, r.client, http.MethodPut, r.workspacePath(state.WorkspaceID.ValueString(), "classifier-rules", state.ID.ValueString()), plan.body())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update classifier rule", err)
		return
	}
	plan.fromAPI(c)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *classifierRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state classifierRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Do(ctx, http.MethodDelete, r.workspacePath(state.WorkspaceID.ValueString(), "classifier-rules", state.ID.ValueString()), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not delete classifier rule "+state.Slug.ValueString(), err)
	}
}

// ImportState takes `<workspace_id>/<id>`, e.g. `ws_3/clf_12`.
func (r *classifierRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req, resp, "clf")
}
