package provider

import (
	"context"
	"net/http"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewPromptShieldRuleResource) }

var (
	_ resource.ResourceWithConfigure      = (*promptShieldRuleResource)(nil)
	_ resource.ResourceWithImportState    = (*promptShieldRuleResource)(nil)
	_ resource.ResourceWithValidateConfig = (*promptShieldRuleResource)(nil)
)

// apiPromptShieldRule is the PromptShieldRule schema of the API: a keyword
// rule carries keywords, a regex rule carries pattern.
type apiPromptShieldRule struct {
	ID          string   `json:"id"`
	WorkspaceID string   `json:"workspace_id"`
	ProjectID   *string  `json:"project_id"`
	Name        string   `json:"name"`
	Slug        string   `json:"slug"`
	Category    string   `json:"category"`
	Detector    string   `json:"detector"`
	Pattern     *string  `json:"pattern"`
	Keywords    []string `json:"keywords"`
	Action      string   `json:"action"`
	Severity    string   `json:"severity"`
	Enabled     bool     `json:"enabled"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type promptShieldRuleModel struct {
	ID          types.String      `tfsdk:"id"`
	WorkspaceID types.String      `tfsdk:"workspace_id"`
	ProjectID   types.String      `tfsdk:"project_id"`
	Name        types.String      `tfsdk:"name"`
	Slug        types.String      `tfsdk:"slug"`
	Category    types.String      `tfsdk:"category"`
	Detector    types.String      `tfsdk:"detector"`
	Pattern     types.String      `tfsdk:"pattern"`
	Keywords    types.Set         `tfsdk:"keywords"`
	Action      types.String      `tfsdk:"action"`
	Severity    types.String      `tfsdk:"severity"`
	Enabled     types.Bool        `tfsdk:"enabled"`
	CreatedAt   timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt   timetypes.RFC3339 `tfsdk:"updated_at"`
}

func (m *promptShieldRuleModel) fromAPI(r apiPromptShieldRule) {
	m.ID = types.StringValue(r.ID)
	m.WorkspaceID = types.StringValue(r.WorkspaceID)
	m.ProjectID = types.StringPointerValue(r.ProjectID)
	m.Name = types.StringValue(r.Name)
	m.Slug = types.StringValue(r.Slug)
	m.Category = types.StringValue(r.Category)
	m.Detector = types.StringValue(r.Detector)
	m.Pattern = types.StringPointerValue(r.Pattern)
	m.Keywords = stringSet(r.Keywords)
	m.Action = types.StringValue(r.Action)
	m.Severity = types.StringValue(r.Severity)
	m.Enabled = types.BoolValue(r.Enabled)
	m.CreatedAt = rfc3339(r.CreatedAt)
	m.UpdatedAt = rfc3339(r.UpdatedAt)
}

// body is the full PromptShieldRuleInput, carrying only the payload key that
// belongs to the detector (the other is a 422).
func (m *promptShieldRuleModel) body(ctx context.Context) (map[string]any, diag.Diagnostics) {
	b := map[string]any{
		"name":       m.Name.ValueString(),
		"slug":       m.Slug.ValueString(),
		"category":   m.Category.ValueString(),
		"detector":   m.Detector.ValueString(),
		"action":     m.Action.ValueString(),
		"severity":   m.Severity.ValueString(),
		"project_id": nullableString(m.ProjectID),
		"enabled":    m.Enabled.ValueBool(),
	}
	if m.Detector.ValueString() == "keyword" {
		var kws []string
		if d := m.Keywords.ElementsAs(ctx, &kws, false); d.HasError() {
			return nil, d
		}
		b["keywords"] = kws
	} else {
		b["pattern"] = m.Pattern.ValueString()
	}
	return b, nil
}

const (
	promptShieldRuleDescription        = "A prompt-shield rule: detects prompt-injection attempts in a workspace's requests, by keyword list or regular expression, and logs, warns, sanitises or blocks them."
	promptShieldRuleIDDescription      = "Public id of the rule (`shield_…`)."
	promptShieldRuleProjectDescription = "Id of a project in the workspace (`proj_…`) to scope the rule to. Omit to apply it to every project in the workspace."
	promptShieldRuleNameDescription    = "Display name, 1–80 characters, unique among the workspace's prompt-shield rules."
	promptShieldRuleSlugDescription    = "The rule's identity in traces: lowercase letters and digits joined by single underscores, at most 64 characters, unique among the workspace's prompt-shield rules. Changing it renames the rule in traces."
	promptShieldRuleCategoryDesc       = "The kind of attack: `instruction_override`, `role_play`, `system_leak`, `indirect_injection` or `tool_abuse`."
	promptShieldRuleDetectorDesc       = "How the rule matches: `keyword` (set `keywords`) or `regex` (set `pattern`)."
	promptShieldRulePatternDesc        = "RE2 regular expression (lookarounds and backreferences are refused), at most 2000 characters. Required when `detector` is `regex`; not allowed for `keyword`."
	promptShieldRuleKeywordsDesc       = "Case-insensitive substrings to match: a set, so order does not matter. At least one; each non-empty, without commas or leading or trailing whitespace, and at most 2000 bytes (UTF-8) in total, counting a comma between each. Required when `detector` is `keyword`; not allowed for `regex`."
	promptShieldRuleActionDesc         = "What to do on a match: `log`, `warn`, `sanitize` or `block`."
	promptShieldRuleSeverityDesc       = "Severity recorded with a match: `low`, `medium` or `high`."
	promptShieldRuleEnabledDesc        = "Whether the rule is enforced. Defaults to `true`; removing the argument enables the rule again."
)

type promptShieldRuleResource struct{ resourceBase }

// NewPromptShieldRuleResource is the routeur_prompt_shield_rule resource.
func NewPromptShieldRuleResource() resource.Resource {
	return &promptShieldRuleResource{resourceBase{name: "prompt_shield_rule"}}
}

func (r *promptShieldRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: promptShieldRuleDescription,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute(promptShieldRuleIDDescription),
			"workspace_id": workspaceIDAttribute(),
			"project_id": schema.StringAttribute{
				MarkdownDescription: promptShieldRuleProjectDescription,
				Optional:            true,
				Validators:          []validator.String{publicID("proj")},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: promptShieldRuleNameDescription,
				Required:            true,
				Validators:          name(80),
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: promptShieldRuleSlugDescription,
				Required:            true,
				Validators:          underscoreSlug(),
			},
			"category": schema.StringAttribute{
				MarkdownDescription: promptShieldRuleCategoryDesc,
				Required:            true,
				Validators: []validator.String{stringvalidator.OneOf(
					"instruction_override", "role_play", "system_leak", "indirect_injection", "tool_abuse",
				)},
			},
			"detector": schema.StringAttribute{
				MarkdownDescription: promptShieldRuleDetectorDesc,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("keyword", "regex")},
			},
			"pattern": schema.StringAttribute{
				MarkdownDescription: promptShieldRulePatternDesc,
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 2000), re2Pattern()},
			},
			"keywords": schema.SetAttribute{
				MarkdownDescription: promptShieldRuleKeywordsDesc,
				Optional:            true,
				ElementType:         types.StringType,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(
						stringvalidator.RegexMatches(regexp.MustCompile(`^[^,]*$`), "must not contain a comma"),
						noSurroundingWhitespace(),
					),
				},
			},
			"action": schema.StringAttribute{
				MarkdownDescription: promptShieldRuleActionDesc,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("log", "warn", "sanitize", "block")},
			},
			"severity": schema.StringAttribute{
				MarkdownDescription: promptShieldRuleSeverityDesc,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("low", "medium", "high")},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: promptShieldRuleEnabledDesc,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

// ValidateConfig ties the payload to the detector at plan time: keyword
// rules take keywords and regex rules take pattern, never both.
func (r *promptShieldRuleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var detector, pattern types.String
	var keywords types.Set
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("detector"), &detector)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("pattern"), &pattern)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("keywords"), &keywords)...)
	if resp.Diagnostics.HasError() || detector.IsUnknown() || detector.IsNull() {
		return
	}
	switch detector.ValueString() {
	// A value known only at apply (from another resource) is neither
	// required-and-missing nor present-and-unexpected yet: the API checks it.
	case "keyword":
		if keywords.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("keywords"), "Missing keywords", `keywords is required when detector is "keyword".`)
		}
		if !pattern.IsNull() && !pattern.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("pattern"), "Unexpected pattern", `pattern can only be set when detector is "regex"; use keywords.`)
		}
		if !keywords.IsNull() && !keywords.IsUnknown() {
			var kws []types.String
			if d := keywords.ElementsAs(ctx, &kws, false); !d.HasError() {
				total, known := 0, true
				for _, k := range kws {
					if k.IsUnknown() {
						known = false
						break
					}
					total += len(k.ValueString()) // bytes, as the API counts them
				}
				if known && total+len(kws)-1 > 2000 {
					resp.Diagnostics.AddAttributeError(path.Root("keywords"), "Keywords too long", "The keywords may total at most 2000 bytes (UTF-8), counting one comma between each, as the API counts them.")
				}
			}
		}
	case "regex":
		if pattern.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("pattern"), "Missing pattern", `pattern is required when detector is "regex".`)
		}
		if !keywords.IsNull() && !keywords.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("keywords"), "Unexpected keywords", `keywords can only be set when detector is "keyword"; use pattern.`)
		}
	}
}

func (r *promptShieldRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan promptShieldRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := plan.body(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := client.Send[apiPromptShieldRule](ctx, r.client, http.MethodPost, r.workspacePath(plan.WorkspaceID.ValueString(), "prompt-shield-rules"), body)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not create prompt-shield rule", err)
		return
	}
	plan.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *promptShieldRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state promptShieldRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := client.Get[apiPromptShieldRule](ctx, r.client, r.workspacePath(state.WorkspaceID.ValueString(), "prompt-shield-rules", state.ID.ValueString()))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read prompt-shield rule", err)
		return
	}
	state.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *promptShieldRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state promptShieldRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := plan.body(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := client.Send[apiPromptShieldRule](ctx, r.client, http.MethodPut, r.workspacePath(state.WorkspaceID.ValueString(), "prompt-shield-rules", state.ID.ValueString()), body)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update prompt-shield rule", err)
		return
	}
	plan.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *promptShieldRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state promptShieldRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Do(ctx, http.MethodDelete, r.workspacePath(state.WorkspaceID.ValueString(), "prompt-shield-rules", state.ID.ValueString()), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not delete prompt-shield rule "+state.Slug.ValueString(), err)
	}
}

// ImportState takes `<workspace_id>/<id>`, e.g. `ws_3/shield_7`.
func (r *promptShieldRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req, resp, "shield")
}
