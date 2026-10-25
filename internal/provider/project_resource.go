package provider

import (
	"context"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewProjectResource) }

var (
	_ resource.ResourceWithConfigure   = (*projectResource)(nil)
	_ resource.ResourceWithImportState = (*projectResource)(nil)
)

// apiProject is the Project schema of the API.
type apiProject struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Description *string `json:"description"`
	IsDefault   bool    `json:"is_default"`
	Enabled     bool    `json:"enabled"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// projectModel is shared by the resource and the data source.
type projectModel struct {
	ID          types.String      `tfsdk:"id"`
	WorkspaceID types.String      `tfsdk:"workspace_id"`
	Name        types.String      `tfsdk:"name"`
	Slug        types.String      `tfsdk:"slug"`
	Description types.String      `tfsdk:"description"`
	IsDefault   types.Bool        `tfsdk:"is_default"`
	Enabled     types.Bool        `tfsdk:"enabled"`
	CreatedAt   timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt   timetypes.RFC3339 `tfsdk:"updated_at"`
}

func (m *projectModel) fromAPI(p apiProject) {
	m.ID = types.StringValue(p.ID)
	m.WorkspaceID = types.StringValue(p.WorkspaceID)
	m.Name = types.StringValue(p.Name)
	m.Slug = types.StringValue(p.Slug)
	m.Description = types.StringPointerValue(p.Description)
	m.IsDefault = types.BoolValue(p.IsDefault)
	m.Enabled = types.BoolValue(p.Enabled)
	m.CreatedAt = rfc3339(p.CreatedAt)
	m.UpdatedAt = rfc3339(p.UpdatedAt)
}

// Descriptions shared with the data source.
const (
	projectDescription            = "A project in a workspace. API keys belong to one project, and routing rules, policies and budgets can be scoped to one."
	projectIDDescription          = "Public id of the project (`proj_…`)."
	projectSlugDescription        = "Unique in the organisation. Lowercase letters, digits and hyphens, at most 60 characters. Set at create only; changing it replaces the project."
	projectNameDescription        = "Display name, 1–80 characters, without leading or trailing whitespace."
	projectDescriptionDescription = "Free-text description, at most 255 characters. Stored verbatim; `\"\"` stays `\"\"`."
	projectIsDefaultDescription   = "Whether this is the workspace's default project, which cannot be disabled or deleted."
	projectEnabledDescription     = "Whether the project is enabled. Defaults to `true`; removing the argument enables the project again."
)

type projectResource struct{ resourceBase }

// NewProjectResource is the routeur_project resource.
func NewProjectResource() resource.Resource {
	return &projectResource{resourceBase{name: "project"}}
}

func (r *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: projectDescription,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute(projectIDDescription),
			"workspace_id": workspaceIDAttribute(),
			"slug": schema.StringAttribute{
				MarkdownDescription: projectSlugDescription,
				Required:            true,
				Validators:          hyphenSlug(projectSlugMax),
				PlanModifiers:       createOnly(),
			},
			"name": schema.StringAttribute{
				MarkdownDescription: projectNameDescription,
				Required:            true,
				Validators:          name(80),
			},
			"description": schema.StringAttribute{
				MarkdownDescription: projectDescriptionDescription,
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(255)},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: projectEnabledDescription,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"is_default": schema.BoolAttribute{
				MarkdownDescription: projectIsDefaultDescription,
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

func (r *projectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectModel
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
	body["enabled"] = plan.Enabled.ValueBool()

	p, err := client.Send[apiProject](ctx, r.client, http.MethodPost, r.workspacePath(plan.WorkspaceID.ValueString(), "projects"), body)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not create project", err)
		return
	}
	plan.fromAPI(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *projectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	p, err := client.Get[apiProject](ctx, r.client, r.workspacePath(state.WorkspaceID.ValueString(), "projects", state.ID.ValueString()))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read project", err)
		return
	}
	state.fromAPI(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *projectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state projectModel
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
	if !plan.Enabled.Equal(state.Enabled) {
		body["enabled"] = plan.Enabled.ValueBool()
	}

	p, err := client.Send[apiProject](ctx, r.client, http.MethodPatch, r.workspacePath(state.WorkspaceID.ValueString(), "projects", state.ID.ValueString()), body)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update project", err)
		return
	}
	plan.fromAPI(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *projectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Do(ctx, http.MethodDelete, r.workspacePath(state.WorkspaceID.ValueString(), "projects", state.ID.ValueString()), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not delete project "+state.Slug.ValueString(), err)
	}
}

// ImportState takes `<workspace_id>/<id>`, e.g. `ws_3/proj_12`.
func (r *projectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req, resp, "proj")
}
