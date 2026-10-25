package provider

import (
	"context"
	"net/http"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewDlpPolicyResource) }

var (
	_ resource.ResourceWithConfigure      = (*dlpPolicyResource)(nil)
	_ resource.ResourceWithImportState    = (*dlpPolicyResource)(nil)
	_ resource.ResourceWithValidateConfig = (*dlpPolicyResource)(nil)
)

// apiDlpPolicy is the DlpPolicy schema of the API: only the payload key that
// belongs to the detector is present.
type apiDlpPolicy struct {
	ID          string   `json:"id"`
	WorkspaceID string   `json:"workspace_id"`
	ProjectID   *string  `json:"project_id"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Detector    string   `json:"detector"`
	Pattern     *string  `json:"pattern"`
	Presets     []string `json:"presets"`
	Keywords    []string `json:"keywords"`
	Secrets     []string `json:"secrets"`
	Action      string   `json:"action"`
	Severity    string   `json:"severity"`
	Enabled     bool     `json:"enabled"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type dlpPolicyModel struct {
	ID          types.String      `tfsdk:"id"`
	WorkspaceID types.String      `tfsdk:"workspace_id"`
	ProjectID   types.String      `tfsdk:"project_id"`
	Slug        types.String      `tfsdk:"slug"`
	Name        types.String      `tfsdk:"name"`
	Detector    types.String      `tfsdk:"detector"`
	Pattern     types.String      `tfsdk:"pattern"`
	Presets     types.Set         `tfsdk:"presets"`
	Keywords    types.Set         `tfsdk:"keywords"`
	Secrets     types.Set         `tfsdk:"secrets"`
	Action      types.String      `tfsdk:"action"`
	Severity    types.String      `tfsdk:"severity"`
	Enabled     types.Bool        `tfsdk:"enabled"`
	CreatedAt   timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt   timetypes.RFC3339 `tfsdk:"updated_at"`
}

// stringSet is a set of strings, or null when the API sent none. The API
// keeps the order it was given, but it carries no meaning, so a reorder on
// either side is no diff.
func stringSet(v []string) types.Set {
	if len(v) == 0 {
		return types.SetNull(types.StringType)
	}
	elems := make([]attr.Value, len(v))
	for i, s := range v {
		elems[i] = types.StringValue(s)
	}
	return types.SetValueMust(types.StringType, elems)
}

// setStrings returns the elements of a known set of strings.
func setStrings(s types.Set) []string {
	out := make([]string, 0, len(s.Elements()))
	for _, e := range s.Elements() {
		if v, ok := e.(types.String); ok {
			out = append(out, v.ValueString())
		}
	}
	return out
}

func (m *dlpPolicyModel) fromAPI(p apiDlpPolicy) {
	m.ID = types.StringValue(p.ID)
	m.WorkspaceID = types.StringValue(p.WorkspaceID)
	m.ProjectID = types.StringPointerValue(p.ProjectID)
	m.Slug = types.StringValue(p.Slug)
	m.Name = types.StringValue(p.Name)
	m.Detector = types.StringValue(p.Detector)
	m.Pattern = types.StringPointerValue(p.Pattern)
	m.Presets = stringSet(p.Presets)
	m.Keywords = stringSet(p.Keywords)
	m.Secrets = stringSet(p.Secrets)
	m.Action = types.StringValue(p.Action)
	m.Severity = types.StringValue(p.Severity)
	m.Enabled = types.BoolValue(p.Enabled)
	m.CreatedAt = rfc3339(p.CreatedAt)
	m.UpdatedAt = rfc3339(p.UpdatedAt)
}

// body is the full desired policy, for POST and PUT alike: PUT replaces, so
// project_id is always sent (null = every project) and so is enabled. Only
// the payload key of the chosen detector is sent; any other is a 422.
func (m *dlpPolicyModel) body() map[string]any {
	body := map[string]any{
		"project_id": nullableString(m.ProjectID),
		"slug":       m.Slug.ValueString(),
		"name":       m.Name.ValueString(),
		"detector":   m.Detector.ValueString(),
		"action":     m.Action.ValueString(),
		"severity":   m.Severity.ValueString(),
		"enabled":    m.Enabled.ValueBool(),
	}
	switch m.Detector.ValueString() {
	case "regex":
		body["pattern"] = m.Pattern.ValueString()
	case "preset":
		body["presets"] = setStrings(m.Presets)
	case "keyword":
		body["keywords"] = setStrings(m.Keywords)
	case "secret":
		body["secrets"] = setStrings(m.Secrets)
	}
	return body
}

const (
	dlpPolicyDescription = "A data-loss-prevention policy in a workspace. It scans prompts (and responses) for a pattern, a managed preset, keywords or secret formats, and applies an action when something matches. " +
		"Policies at workspace and project level compose: a call is matched by its workspace's policies and its project's. " +
		"Choose the detector with `detector` and set the one payload argument that belongs to it: `pattern` for `regex`, `presets` for `preset`, `keywords` for `keyword`, `secrets` for `secret`."
	dlpIDDescription       = "Public id of the policy (`dlp_…`)."
	dlpProjectDescription  = "Scope the policy to one project in this workspace (`proj_…`). Omit it to apply to every project in the workspace. Changing it updates the policy in place."
	dlpSlugDescription     = "Unique in the workspace, lowercase letters and digits joined by single underscores, at most 64 characters. It is the policy's identifier in a trace's `dlp_matched_policies`, so changing it renames that identifier."
	dlpNameDescription     = "Unique in the workspace, 1–80 characters, without leading or trailing whitespace."
	dlpDetectorDescription = "What the policy detects: `regex`, `preset`, `keyword` or `secret`. Set the matching payload argument (`pattern`, `presets`, `keywords` or `secrets`) and no other."
	dlpPatternDescription  = "RE2 regular expression (not PCRE: no lookarounds or backreferences), at most 2000 characters. Stored verbatim. Only with `detector = \"regex\"`."
	dlpPresetsDescription  = "Managed preset ids: `email`, `phone`, `credit_card`, `iban`, `us_aba_routing`, `swift_bic`, `us_ssn`, `us_itin`, `uk_nhs_number`, `uk_nino`, `es_dni`, `it_fiscal_code`, `in_aadhaar`, `ca_sin` or `ip_address`; " +
		"older policies may also use `aws_key`, `github_token`, `jwt` and `private_key`, but credentials are better matched with `detector = \"secret\"`. " +
		"See the [presets reference](https://docs.routeur.ai/guides/presets#dlp) for what each one matches. New presets may be added; the API refuses an unknown id. A set: order does not matter. Items have no commas or surrounding whitespace. Only with `detector = \"preset\"`."
	dlpKeywordsDescription  = "Case-insensitive substrings to match. A set: order does not matter. Items have no commas or surrounding whitespace. Only with `detector = \"keyword\"`."
	dlpSecretsDescription   = "Secret-format ids, such as `aws-access-token`, `github-pat` or `1password-secret-key`. Every selectable id is listed in [Secret formats](https://docs.routeur.ai/guides/secret-formats); the API refuses an unknown id. A set: order does not matter. Items have no commas or surrounding whitespace. Only with `detector = \"secret\"`."
	dlpActionDescription    = "What happens on a match: `log`, `warn`, `redact`, `tokenize` or `block`. `tokenize` (reversible redaction) needs a plan that includes it, otherwise the API refuses the policy."
	dlpSeverityDescription  = "`low`, `medium` or `high`."
	dlpEnabledDescription   = "Whether the policy is enforced. Defaults to `true`; removing the argument enables the policy again."
	dlpListItemMessage      = "must not contain a comma"
	dlpListItemPatternRegex = `^[^,]*$` // blank items: noSurroundingWhitespace
)

type dlpPolicyResource struct{ resourceBase }

// NewDlpPolicyResource is the routeur_dlp_policy resource.
func NewDlpPolicyResource() resource.Resource {
	return &dlpPolicyResource{resourceBase{name: "dlp_policy"}}
}

func dlpSetValidators() []validator.Set {
	return []validator.Set{
		setvalidator.SizeAtLeast(1),
		setvalidator.ValueStringsAre(
			stringvalidator.RegexMatches(regexp.MustCompile(dlpListItemPatternRegex), dlpListItemMessage),
			noSurroundingWhitespace(),
		),
	}
}

func (r *dlpPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: dlpPolicyDescription,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute(dlpIDDescription),
			"workspace_id": workspaceIDAttribute(),
			"project_id": schema.StringAttribute{
				MarkdownDescription: dlpProjectDescription,
				Optional:            true,
				Validators:          []validator.String{publicID("proj")},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: dlpSlugDescription,
				Required:            true,
				Validators:          underscoreSlug(),
			},
			"name": schema.StringAttribute{
				MarkdownDescription: dlpNameDescription,
				Required:            true,
				Validators:          name(80),
			},
			"detector": schema.StringAttribute{
				MarkdownDescription: dlpDetectorDescription,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("regex", "preset", "keyword", "secret")},
			},
			"pattern": schema.StringAttribute{
				MarkdownDescription: dlpPatternDescription,
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 2000), re2Pattern()},
			},
			"presets": schema.SetAttribute{
				MarkdownDescription: dlpPresetsDescription,
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          dlpSetValidators(),
			},
			"keywords": schema.SetAttribute{
				MarkdownDescription: dlpKeywordsDescription,
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          dlpSetValidators(),
			},
			"secrets": schema.SetAttribute{
				MarkdownDescription: dlpSecretsDescription,
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          dlpSetValidators(),
			},
			"action": schema.StringAttribute{
				MarkdownDescription: dlpActionDescription,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("log", "warn", "redact", "tokenize", "block")},
			},
			"severity": schema.StringAttribute{
				MarkdownDescription: dlpSeverityDescription,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("low", "medium", "high")},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: dlpEnabledDescription,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

// ValidateConfig requires the payload argument of the chosen detector and
// refuses the others, so a wrong combination fails at plan time.
func (r *dlpPolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg dlpPolicyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Detector.IsNull() || cfg.Detector.IsUnknown() {
		return
	}

	payloads := []struct {
		detector, attr string
		null           bool
		unknown        bool
	}{
		{"regex", "pattern", cfg.Pattern.IsNull(), cfg.Pattern.IsUnknown()},
		{"preset", "presets", cfg.Presets.IsNull(), cfg.Presets.IsUnknown()},
		{"keyword", "keywords", cfg.Keywords.IsNull(), cfg.Keywords.IsUnknown()},
		{"secret", "secrets", cfg.Secrets.IsNull(), cfg.Secrets.IsUnknown()},
	}
	chosen := cfg.Detector.ValueString()
	for _, p := range payloads {
		if p.unknown {
			continue // known only at apply (from another resource): the API checks it then
		}
		switch {
		case p.detector == chosen && p.null:
			resp.Diagnostics.AddAttributeError(path.Root(p.attr), "Missing payload for detector",
				"`"+p.attr+"` is required when `detector` is \""+chosen+"\".")
		case p.detector != chosen && !p.null:
			resp.Diagnostics.AddAttributeError(path.Root(p.attr), "Payload does not match detector",
				"`"+p.attr+"` can only be set when `detector` is \""+p.detector+"\", not \""+chosen+"\".")
		}
	}
}

func (r *dlpPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dlpPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	p, err := client.Send[apiDlpPolicy](ctx, r.client, http.MethodPost, r.workspacePath(plan.WorkspaceID.ValueString(), "dlp-policies"), plan.body())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not create DLP policy", err)
		return
	}
	plan.fromAPI(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *dlpPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dlpPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	p, err := client.Get[apiDlpPolicy](ctx, r.client, r.workspacePath(state.WorkspaceID.ValueString(), "dlp-policies", state.ID.ValueString()))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read DLP policy", err)
		return
	}
	state.fromAPI(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *dlpPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state dlpPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	p, err := client.Send[apiDlpPolicy](ctx, r.client, http.MethodPut, r.workspacePath(state.WorkspaceID.ValueString(), "dlp-policies", state.ID.ValueString()), plan.body())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update DLP policy", err)
		return
	}
	plan.fromAPI(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *dlpPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dlpPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Do(ctx, http.MethodDelete, r.workspacePath(state.WorkspaceID.ValueString(), "dlp-policies", state.ID.ValueString()), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not delete DLP policy "+state.Slug.ValueString(), err)
	}
}

// ImportState takes `<workspace_id>/<id>`, e.g. `ws_3/dlp_12`.
func (r *dlpPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req, resp, "dlp")
}
