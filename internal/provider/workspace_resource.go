package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewWorkspaceResource) }

var (
	_ resource.ResourceWithConfigure   = (*workspaceResource)(nil)
	_ resource.ResourceWithImportState = (*workspaceResource)(nil)
)

// apiWorkspace is the Workspace schema of the API.
type apiWorkspace struct {
	ID               string  `json:"id"`
	Slug             string  `json:"slug"`
	Name             string  `json:"name"`
	Description      *string `json:"description"`
	DefaultProjectID *string `json:"default_project_id"`
	IsDefault        bool    `json:"is_default"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
}

// workspaceModel is shared by the resource and the data source.
type workspaceModel struct {
	ID               types.String      `tfsdk:"id"`
	Slug             types.String      `tfsdk:"slug"`
	Name             types.String      `tfsdk:"name"`
	Description      types.String      `tfsdk:"description"`
	DefaultProjectID types.String      `tfsdk:"default_project_id"`
	IsDefault        types.Bool        `tfsdk:"is_default"`
	CreatedAt        timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt        timetypes.RFC3339 `tfsdk:"updated_at"`
}

func (m *workspaceModel) fromAPI(w apiWorkspace) {
	m.ID = types.StringValue(w.ID)
	m.Slug = types.StringValue(w.Slug)
	m.Name = types.StringValue(w.Name)
	m.Description = types.StringPointerValue(w.Description)
	m.DefaultProjectID = types.StringPointerValue(w.DefaultProjectID)
	m.IsDefault = types.BoolValue(w.IsDefault)
	m.CreatedAt = rfc3339(w.CreatedAt)
	m.UpdatedAt = rfc3339(w.UpdatedAt)
}

// Descriptions shared with the data source.
const (
	workspaceDescription = "A workspace: the default scope for routing rules and policies. Creating one also creates its default project and its Default route; deleting it removes them too. " +
		"A workspace created through the API (and so by Terraform) gets a default project with the slug `<workspace slug>-core`; the workspace an organisation starts with has one called `core`. " +
		"Refer to the default project by `default_project_id` (or the `routeur_project` data source with that id) rather than by guessing its slug."
	workspaceIDDescription          = "Public id of the workspace (`ws_…`). Pass it as `workspace_id` to resources that live in the workspace."
	workspaceSlugDescription        = "Unique in the organisation: the workspace's URL identity. Lowercase letters, digits and hyphens, at most 55 characters (the default project is created as `<slug>-core`). Set at create only; changing it replaces the workspace."
	workspaceNameDescription        = "Display name, 1–80 characters, without leading or trailing whitespace."
	workspaceDescriptionDescription = "Free-text description, at most 255 characters. Stored verbatim; `\"\"` stays `\"\"`."
	workspaceDefaultProjectDesc     = "Id of the workspace's default project (`proj_…`): where a new API key lands when it names no project. Null only for a workspace that has none."
	workspaceIsDefaultDescription   = "Whether this is the organisation's default workspace, which cannot be deleted."
)

type workspaceResource struct{ resourceBase }

// NewWorkspaceResource is the routeur_workspace resource.
func NewWorkspaceResource() resource.Resource {
	return &workspaceResource{resourceBase{name: "workspace"}}
}

func (r *workspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: workspaceDescription,
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(workspaceIDDescription),
			"slug": schema.StringAttribute{
				MarkdownDescription: workspaceSlugDescription,
				Required:            true,
				Validators:          hyphenSlug(workspaceSlugMax),
				PlanModifiers:       createOnly(),
			},
			"name": schema.StringAttribute{
				MarkdownDescription: workspaceNameDescription,
				Required:            true,
				Validators:          name(80),
			},
			"description": schema.StringAttribute{
				MarkdownDescription: workspaceDescriptionDescription,
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(255)},
			},
			"default_project_id": schema.StringAttribute{
				MarkdownDescription: workspaceDefaultProjectDesc,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"is_default": schema.BoolAttribute{
				MarkdownDescription: workspaceIsDefaultDescription,
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

func (r *workspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := map[string]any{
		"name": plan.Name.ValueString(),
		"slug": plan.Slug.ValueString(),
	}
	if !plan.Description.IsNull() {
		body["description"] = plan.Description.ValueString()
	}

	ws, err := client.Send[apiWorkspace](ctx, r.client, http.MethodPost, r.orgPath("workspaces"), body)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not create workspace", err)
		return
	}
	plan.fromAPI(ws)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *workspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workspaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ws, err := client.Get[apiWorkspace](ctx, r.client, r.workspacePath(state.ID.ValueString()))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read workspace", err)
		return
	}
	state.fromAPI(ws)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *workspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state workspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// PATCH: send only what changed; null clears the description.
	body := map[string]any{}
	if !plan.Name.Equal(state.Name) {
		body["name"] = plan.Name.ValueString()
	}
	if !plan.Description.Equal(state.Description) {
		body["description"] = nullableString(plan.Description)
	}

	ws, err := client.Send[apiWorkspace](ctx, r.client, http.MethodPatch, r.workspacePath(state.ID.ValueString()), body)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update workspace", err)
		return
	}
	plan.fromAPI(ws)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *workspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state workspaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Do(ctx, http.MethodDelete, r.workspacePath(state.ID.ValueString()), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not delete workspace "+state.Slug.ValueString(), err)
	}
}

// ImportState accepts the id (ws_3) or the slug; Read stores the id.
func (r *workspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !publicIDPattern("ws").MatchString(req.ID) && (len(req.ID) > 60 || !hyphenSlugPattern.MatchString(req.ID)) {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("expected a workspace id such as ws_3 or a workspace slug such as production, got %q.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
