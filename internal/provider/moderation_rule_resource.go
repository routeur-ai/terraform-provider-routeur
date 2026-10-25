package provider

import (
	"context"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewModerationRuleResource) }

var (
	_ resource.ResourceWithConfigure      = (*moderationRuleResource)(nil)
	_ resource.ResourceWithImportState    = (*moderationRuleResource)(nil)
	_ resource.ResourceWithValidateConfig = (*moderationRuleResource)(nil)
)

// apiModerationRule is the ModerationRule schema of the API.
type apiModerationRule struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	ProjectID   *string `json:"project_id"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Category    string  `json:"category"`
	Pattern     *string `json:"pattern"`
	Action      string  `json:"action"`
	Severity    string  `json:"severity"`
	Streaming   *string `json:"streaming"`
	Enabled     bool    `json:"enabled"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type moderationRuleModel struct {
	ID          types.String      `tfsdk:"id"`
	WorkspaceID types.String      `tfsdk:"workspace_id"`
	ProjectID   types.String      `tfsdk:"project_id"`
	Name        types.String      `tfsdk:"name"`
	Slug        types.String      `tfsdk:"slug"`
	Category    types.String      `tfsdk:"category"`
	Pattern     types.String      `tfsdk:"pattern"`
	Action      types.String      `tfsdk:"action"`
	Severity    types.String      `tfsdk:"severity"`
	Streaming   types.String      `tfsdk:"streaming"`
	Enabled     types.Bool        `tfsdk:"enabled"`
	CreatedAt   timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt   timetypes.RFC3339 `tfsdk:"updated_at"`
}

func (m *moderationRuleModel) fromAPI(r apiModerationRule) {
	m.ID = types.StringValue(r.ID)
	m.WorkspaceID = types.StringValue(r.WorkspaceID)
	m.ProjectID = types.StringPointerValue(r.ProjectID)
	m.Name = types.StringValue(r.Name)
	m.Slug = types.StringValue(r.Slug)
	m.Category = types.StringValue(r.Category)
	m.Pattern = types.StringPointerValue(r.Pattern)
	m.Action = types.StringValue(r.Action)
	m.Severity = types.StringValue(r.Severity)
	m.Streaming = types.StringPointerValue(r.Streaming)
	m.Enabled = types.BoolValue(r.Enabled)
	m.CreatedAt = rfc3339(r.CreatedAt)
	m.UpdatedAt = rfc3339(r.UpdatedAt)
}

// body is the full ModerationRuleInput: PUT replaces the rule, and an omitted
// pattern, streaming or project_id is cleared, so null attributes are sent
// as null.
func (m *moderationRuleModel) body() map[string]any {
	return map[string]any{
		"name":       m.Name.ValueString(),
		"slug":       m.Slug.ValueString(),
		"category":   m.Category.ValueString(),
		"pattern":    nullableString(m.Pattern),
		"action":     m.Action.ValueString(),
		"severity":   m.Severity.ValueString(),
		"streaming":  nullableString(m.Streaming),
		"project_id": nullableString(m.ProjectID),
		"enabled":    m.Enabled.ValueBool(),
	}
}

const (
	moderationRuleDescription          = "An output-moderation rule: scans model responses in a workspace and logs, redacts, rewrites or blocks matches. Requires a routeur.ai plan with output moderation to create or change rules."
	moderationRuleIDDescription        = "Public id of the rule (`mod_…`)."
	moderationRuleProjectDescription   = "Id of a project in the workspace (`proj_…`) to scope the rule to. Omit to apply it to every project in the workspace."
	moderationRuleNameDescription      = "Display name, 1–80 characters, unique among the workspace's moderation rules."
	moderationRuleSlugDescription      = "The rule's identity in traces: lowercase letters and digits joined by single underscores, at most 64 characters, unique among the workspace's moderation rules. Changing it renames the rule in traces."
	moderationRuleCategoryDescription  = "What to detect: a managed category (`hate`, `harassment`, `profanity`, `sexual`, `sexual_minors`, `self_harm`, `violence`, `weapons`) or `regex` for a custom `pattern`."
	moderationRulePatternDescription   = "RE2 regular expression (lookarounds and backreferences are refused), at most 2000 characters. Required when `category` is `regex` and must be omitted for every other category."
	moderationRuleActionDescription    = "What to do on a match: `log`, `redact`, `block` or `rewrite`."
	moderationRuleSeverityDescription  = "Severity recorded with a match: `low`, `medium` or `high`."
	moderationRuleStreamingDescription = "Per-rule override of the organisation's streaming default (see `routeur_moderation_settings`): `buffered` or `chunked`. Omit to inherit the organisation default."
	moderationRuleEnabledDescription   = "Whether the rule is enforced. Defaults to `true`; removing the argument enables the rule again."
)

type moderationRuleResource struct{ resourceBase }

// NewModerationRuleResource is the routeur_moderation_rule resource.
func NewModerationRuleResource() resource.Resource {
	return &moderationRuleResource{resourceBase{name: "moderation_rule"}}
}

func (r *moderationRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: moderationRuleDescription,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute(moderationRuleIDDescription),
			"workspace_id": workspaceIDAttribute(),
			"project_id": schema.StringAttribute{
				MarkdownDescription: moderationRuleProjectDescription,
				Optional:            true,
				Validators:          []validator.String{publicID("proj")},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: moderationRuleNameDescription,
				Required:            true,
				Validators:          name(80),
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: moderationRuleSlugDescription,
				Required:            true,
				Validators:          underscoreSlug(),
			},
			"category": schema.StringAttribute{
				MarkdownDescription: moderationRuleCategoryDescription,
				Required:            true,
				Validators: []validator.String{stringvalidator.OneOf(
					"hate", "harassment", "profanity", "sexual", "sexual_minors", "self_harm", "violence", "weapons", "regex",
				)},
			},
			"pattern": schema.StringAttribute{
				MarkdownDescription: moderationRulePatternDescription,
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 2000), re2Pattern()},
			},
			"action": schema.StringAttribute{
				MarkdownDescription: moderationRuleActionDescription,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("log", "redact", "block", "rewrite")},
			},
			"severity": schema.StringAttribute{
				MarkdownDescription: moderationRuleSeverityDescription,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("low", "medium", "high")},
			},
			"streaming": schema.StringAttribute{
				MarkdownDescription: moderationRuleStreamingDescription,
				Optional:            true,
				Validators:          []validator.String{stringvalidator.OneOf("buffered", "chunked")},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: moderationRuleEnabledDescription,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

// ValidateConfig ties pattern to category at plan time: `regex` needs a
// pattern, every managed category refuses one (the API answers 422).
func (r *moderationRuleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var category, pattern types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("category"), &category)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("pattern"), &pattern)...)
	if resp.Diagnostics.HasError() || category.IsUnknown() || category.IsNull() || pattern.IsUnknown() {
		return
	}
	switch {
	case category.ValueString() == "regex" && pattern.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("pattern"), "Missing pattern", `pattern is required when category is "regex".`)
	case category.ValueString() != "regex" && !pattern.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("pattern"), "Unexpected pattern", `pattern can only be set when category is "regex"; the managed categories use routeur.ai's own word lists.`)
	}
}

func (r *moderationRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan moderationRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := client.Send[apiModerationRule](ctx, r.client, http.MethodPost, r.workspacePath(plan.WorkspaceID.ValueString(), "moderation-rules"), plan.body())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not create moderation rule", err)
		return
	}
	plan.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *moderationRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state moderationRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := client.Get[apiModerationRule](ctx, r.client, r.workspacePath(state.WorkspaceID.ValueString(), "moderation-rules", state.ID.ValueString()))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read moderation rule", err)
		return
	}
	state.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *moderationRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state moderationRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := client.Send[apiModerationRule](ctx, r.client, http.MethodPut, r.workspacePath(state.WorkspaceID.ValueString(), "moderation-rules", state.ID.ValueString()), plan.body())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update moderation rule", err)
		return
	}
	plan.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *moderationRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state moderationRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Do(ctx, http.MethodDelete, r.workspacePath(state.WorkspaceID.ValueString(), "moderation-rules", state.ID.ValueString()), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not delete moderation rule "+state.Slug.ValueString(), err)
	}
}

// ImportState takes `<workspace_id>/<id>`, e.g. `ws_3/mod_7`.
func (r *moderationRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req, resp, "mod")
}
