package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewWorkspaceModelResource) }

var (
	_ resource.ResourceWithConfigure   = (*workspaceModelResource)(nil)
	_ resource.ResourceWithImportState = (*workspaceModelResource)(nil)
)

// modelSlugPattern is `<provider>:<model_id>`; the model id may contain `/`
// and `:`, and new providers may appear, so only the shape is checked.
var modelSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*:.+$`)

// apiWorkspaceModel is the WorkspaceModel schema of the API.
type apiWorkspaceModel struct {
	ID            string   `json:"id"`
	Provider      string   `json:"provider"`
	ModelID       string   `json:"model_id"`
	DisplayName   string   `json:"display_name"`
	Active        bool     `json:"active"`
	Catalogued    bool     `json:"catalogued"`
	Availability  *string  `json:"availability"`
	Capabilities  []string `json:"capabilities"`
	ContextTokens *int64   `json:"context_tokens"`
	InputPer1k    *float64 `json:"input_per_1k"`
	OutputPer1k   *float64 `json:"output_per_1k"`
}

// workspaceModelInfo is the read-only description of a model, shared by
// the resource and the routeur_workspace_models data source.
type workspaceModelInfo struct {
	ProviderName  types.String  `tfsdk:"provider_name"`
	ModelID       types.String  `tfsdk:"model_id"`
	DisplayName   types.String  `tfsdk:"display_name"`
	Catalogued    types.Bool    `tfsdk:"catalogued"`
	Availability  types.String  `tfsdk:"availability"`
	Capabilities  types.Set     `tfsdk:"capabilities"`
	ContextTokens types.Int64   `tfsdk:"context_tokens"`
	InputPer1k    types.Float64 `tfsdk:"input_per_1k"`
	OutputPer1k   types.Float64 `tfsdk:"output_per_1k"`
}

func (m *workspaceModelInfo) fromAPI(w apiWorkspaceModel) {
	m.ProviderName = types.StringValue(w.Provider)
	m.ModelID = types.StringValue(w.ModelID)
	m.DisplayName = types.StringValue(w.DisplayName)
	m.Catalogued = types.BoolValue(w.Catalogued)
	m.Availability = types.StringPointerValue(w.Availability)
	caps := make([]attr.Value, 0, len(w.Capabilities))
	for _, c := range w.Capabilities {
		caps = append(caps, types.StringValue(c))
	}
	m.Capabilities = types.SetValueMust(types.StringType, caps)
	m.ContextTokens = types.Int64PointerValue(w.ContextTokens)
	m.InputPer1k = types.Float64PointerValue(w.InputPer1k)
	m.OutputPer1k = types.Float64PointerValue(w.OutputPer1k)
}

type workspaceModelModel struct {
	ID          types.String `tfsdk:"id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	Model       types.String `tfsdk:"model"`
	Active      types.Bool   `tfsdk:"active"`
	workspaceModelInfo
}

func (m *workspaceModelModel) fromAPI(workspaceID string, w apiWorkspaceModel) {
	m.ID = types.StringValue(workspaceID + "/" + w.ID)
	m.WorkspaceID = types.StringValue(workspaceID)
	m.Model = types.StringValue(w.ID)
	m.Active = types.BoolValue(w.Active)
	m.workspaceModelInfo.fromAPI(w)
}

// Descriptions shared with the data source.
const (
	workspaceModelDescription = "Whether a workspace routes to one upstream model. Models are opt-in per workspace: declaring this resource activates the model, " +
		"and destroying it deactivates it again (models themselves are never created or deleted). " +
		"A provider's models are listed once the organisation has an enabled `routeur_provider_credential` for it; the `routeur_workspace_models` data source lists them. " +
		"Build `model` from the credential's `provider_name` (`\"${routeur_provider_credential.openai.provider_name}:gpt-4o\"`) and Terraform creates the credential first and removes it last, with no `depends_on`." +
		"\n\nThe API refuses to deactivate a model that a routing rule in the workspace pins or lists as a failover target (error `model_in_use`, naming the rules), whether that rule is enabled or not. " +
		"Reference this resource's `provider_name` and `model_id` from those rules: Terraform then changes or removes the rules before it deactivates the model."
	workspaceModelSlugDescription = "The model, as `<provider>:<model_id>`, e.g. `openai:gpt-4o` (model ids may contain `/` and `:`, as some fine-tuned and discovered models do). Changing it replaces the resource."
	workspaceModelActiveDesc      = "Whether the workspace routes to the model. Defaults to `true`; set `false` to keep the model managed but deactivated. Deactivating a model a routing rule still uses is refused (`model_in_use`)."
	workspaceModelProviderDesc    = "Provider slug, e.g. `openai`. (The API calls this field `provider`, a name Terraform reserves.)"
	workspaceModelModelIDDesc     = "The upstream model id. May contain `/` or `:`."
	workspaceModelDisplayNameDesc = "Curated name; the model id when uncatalogued."
	workspaceModelCataloguedDesc  = "False for a model discovered at the provider but not yet in routeur.ai's curated catalogue (prices and context may then be null)."
	workspaceModelAvailDesc       = "Last provider discovery result: `available`, `unavailable` or `deprecated`; null if never observed. New values may be added."
	workspaceModelCapsDesc        = "Capabilities from the curated catalogue, e.g. `text`, `tools` (a set: order carries no meaning); empty when uncatalogued."
	workspaceModelContextDesc     = "Context window in tokens; null when unknown."
	workspaceModelInputDesc       = "Input price per 1,000 tokens, in USD; null when unknown."
	workspaceModelOutputDesc      = "Output price per 1,000 tokens, in USD; null when unknown."
)

type workspaceModelResource struct{ resourceBase }

// NewWorkspaceModelResource is the routeur_workspace_model resource.
func NewWorkspaceModelResource() resource.Resource {
	return &workspaceModelResource{resourceBase{name: "workspace_model"}}
}

func (r *workspaceModelResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	stable := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: workspaceModelDescription,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute("`<workspace_id>/<model>`, e.g. `ws_3/openai:gpt-4o`."),
			"workspace_id": workspaceIDAttribute(),
			"model": schema.StringAttribute{
				MarkdownDescription: workspaceModelSlugDescription,
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(modelSlugPattern, "must be <provider>:<model_id>, e.g. openai:gpt-4o")},
				PlanModifiers:       createOnly(),
			},
			"active": schema.BoolAttribute{
				MarkdownDescription: workspaceModelActiveDesc,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			// Catalogue metadata keeps its state value across a plan; Read
			// refreshes it.
			"provider_name":  schema.StringAttribute{MarkdownDescription: workspaceModelProviderDesc, Computed: true, PlanModifiers: stable},
			"model_id":       schema.StringAttribute{MarkdownDescription: workspaceModelModelIDDesc, Computed: true, PlanModifiers: stable},
			"display_name":   schema.StringAttribute{MarkdownDescription: workspaceModelDisplayNameDesc, Computed: true, PlanModifiers: stable},
			"catalogued":     schema.BoolAttribute{MarkdownDescription: workspaceModelCataloguedDesc, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}},
			"availability":   schema.StringAttribute{MarkdownDescription: workspaceModelAvailDesc, Computed: true, PlanModifiers: stable},
			"capabilities":   schema.SetAttribute{MarkdownDescription: workspaceModelCapsDesc, ElementType: types.StringType, Computed: true, PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()}},
			"context_tokens": schema.Int64Attribute{MarkdownDescription: workspaceModelContextDesc, Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"input_per_1k":   schema.Float64Attribute{MarkdownDescription: workspaceModelInputDesc, Computed: true, PlanModifiers: []planmodifier.Float64{float64planmodifier.UseStateForUnknown()}},
			"output_per_1k":  schema.Float64Attribute{MarkdownDescription: workspaceModelOutputDesc, Computed: true, PlanModifiers: []planmodifier.Float64{float64planmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *workspaceModelResource) modelPath(workspaceID, slug string) string {
	return r.workspacePath(workspaceID, "models", slug)
}

// put sets active and stores the result; Create and Update are the same
// idempotent PUT.
func (r *workspaceModelResource) put(ctx context.Context, plan workspaceModelModel, s schemaPaths, summary string) (workspaceModelModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	ws, slug := plan.WorkspaceID.ValueString(), plan.Model.ValueString()
	w, err := client.Send[apiWorkspaceModel](ctx, r.client, http.MethodPut, r.modelPath(ws, slug), map[string]any{"active": plan.Active.ValueBool()})
	if err != nil {
		if client.IsNotFound(err) {
			diags.AddAttributeError(path.Root("model"), summary,
				fmt.Sprintf("routeur.ai does not know the model %q in workspace %s. Check the spelling; a provider's discovered models appear once the organisation has an enabled credential for it (the routeur_workspace_models data source lists them).", slug, ws))
			return plan, diags
		}
		addAPIError(ctx, &diags, s, summary, err, providerNameAlias)
		return plan, diags
	}
	plan.fromAPI(ws, w)
	return plan, diags
}

func (r *workspaceModelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workspaceModelModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan, d := r.put(ctx, plan, req.Plan.Schema, "Could not set model "+plan.Model.ValueString())
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *workspaceModelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workspaceModelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ws := state.WorkspaceID.ValueString()
	w, err := client.Get[apiWorkspaceModel](ctx, r.client, r.modelPath(ws, state.Model.ValueString()))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read model "+state.Model.ValueString(), err, providerNameAlias)
		return
	}
	state.fromAPI(ws, w)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *workspaceModelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan workspaceModelModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Keep the metadata planned from state rather than the PUT response's:
	// different values would be an inconsistent result after apply. The
	// next refresh picks up any change.
	planned := plan.workspaceModelInfo
	plan, d := r.put(ctx, plan, req.Plan.Schema, "Could not update model "+plan.Model.ValueString())
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.workspaceModelInfo = planned
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete deactivates the model: models are opt-in, so inactive is the state
// of a model no configuration manages.
func (r *workspaceModelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state workspaceModelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Do(ctx, http.MethodPut, r.modelPath(state.WorkspaceID.ValueString(), state.Model.ValueString()), map[string]any{"active": false}, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not deactivate model "+state.Model.ValueString(), err, providerNameAlias)
	}
}

// ImportState takes `<workspace_id>/<model>`, e.g. `ws_3/openai:gpt-4o` or
// `ws_3/openai:ft/custom-model` (everything after the first `/` is the model).
func (r *workspaceModelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	pieces, err := splitImportID(req.ID, "workspace_id", "model")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	if !publicIDPattern("ws").MatchString(pieces[0]) {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("workspace_id must be a workspace id such as ws_3, got %q. The routeur_workspace data source resolves a slug to its id.", pieces[0]))
		return
	}
	if !modelSlugPattern.MatchString(pieces[1]) {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("model must be <provider>:<model_id>, e.g. openai:gpt-4o, got %q.", pieces[1]))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), pieces[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("model"), pieces[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
