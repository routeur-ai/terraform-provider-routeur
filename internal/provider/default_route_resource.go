package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewDefaultRouteResource) }

var (
	_ resource.ResourceWithConfigure   = (*defaultRouteResource)(nil)
	_ resource.ResourceWithImportState = (*defaultRouteResource)(nil)
)

// defaultQualityBias is the API default for action.quality_bias.
const defaultQualityBias = 50

type defaultRouteModel struct {
	ID          types.String      `tfsdk:"id"`
	WorkspaceID types.String      `tfsdk:"workspace_id"`
	QualityBias types.Int64       `tfsdk:"quality_bias"`
	Slug        types.String      `tfsdk:"slug"`
	Name        types.String      `tfsdk:"name"`
	Priority    types.Int64       `tfsdk:"priority"`
	CreatedAt   timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt   timetypes.RFC3339 `tfsdk:"updated_at"`
}

func (m *defaultRouteModel) fromAPI(r apiRoutingRule) {
	m.ID = types.StringValue(r.ID)
	m.WorkspaceID = types.StringValue(r.WorkspaceID)
	m.QualityBias = types.Int64PointerValue(r.Action.QualityBias)
	m.Slug = types.StringValue(r.Slug)
	m.Name = types.StringValue(r.Name)
	m.Priority = types.Int64Value(r.Priority)
	m.CreatedAt = rfc3339(r.CreatedAt)
	m.UpdatedAt = rfc3339(r.UpdatedAt)
}

const (
	defaultRouteDescription = "The Default route of a workspace: the always-present auto-routing catch-all that handles every " +
		"request no `routeur_routing_rule` matched. Every workspace has exactly one, created and deleted with the " +
		"workspace; only its quality bias can change. Creating this resource adopts it, and destroying it sets the " +
		"quality bias back to `50` and stops managing it."
	defaultRouteQualityBiasDescription = "0 (cheapest) to 100 (smartest) for the auto-router. Defaults to `50`, which is " +
		"neutral; removing the argument sets it back to `50`."
)

type defaultRouteResource struct{ resourceBase }

// NewDefaultRouteResource is the routeur_default_route resource.
func NewDefaultRouteResource() resource.Resource {
	return &defaultRouteResource{resourceBase{name: "default_route"}}
}

func (r *defaultRouteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	stable := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: defaultRouteDescription,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute("Public id of the Default route (`route_…`)."),
			"workspace_id": workspaceIDAttribute(),
			"quality_bias": schema.Int64Attribute{
				MarkdownDescription: defaultRouteQualityBiasDescription,
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(defaultQualityBias),
				Validators:          []validator.Int64{int64validator.Between(0, 100)},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Server-assigned and immutable (`route_default`); traces record a match as `rule:<slug>`.",
				Computed:            true,
				PlanModifiers:       stable,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name, fixed by routeur.ai (`Default`).",
				Computed:            true,
				PlanModifiers:       stable,
			},
			"priority": schema.Int64Attribute{
				MarkdownDescription: "Evaluation order, set by routeur.ai: `1000` or more, so the Default route always runs after every routing rule.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

func (r *defaultRouteResource) put(ctx context.Context, workspaceID string, bias int64) (apiRoutingRule, error) {
	body := map[string]any{"action": map[string]any{"quality_bias": bias}}
	return client.Send[apiRoutingRule](ctx, r.client, http.MethodPut, r.workspacePath(workspaceID, "default-route"), body)
}

// Create adopts the workspace's Default route: it always exists.
func (r *defaultRouteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan defaultRouteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := r.put(ctx, plan.WorkspaceID.ValueString(), plan.QualityBias.ValueInt64())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not set the Default route", err, defaultRouteErrorAliases)
		return
	}
	plan.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *defaultRouteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state defaultRouteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := client.Get[apiRoutingRule](ctx, r.client, r.workspacePath(state.WorkspaceID.ValueString(), "default-route"))
	if client.IsNotFound(err) { // the workspace is gone
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read the Default route", err)
		return
	}
	state.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *defaultRouteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan defaultRouteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := r.put(ctx, plan.WorkspaceID.ValueString(), plan.QualityBias.ValueInt64())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update the Default route", err, defaultRouteErrorAliases)
		return
	}
	plan.fromAPI(rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete sets the quality bias back to the API default; the Default route
// itself cannot be deleted and goes with its workspace.
func (r *defaultRouteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state defaultRouteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.put(ctx, state.WorkspaceID.ValueString(), defaultQualityBias)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not reset the Default route", err, defaultRouteErrorAliases)
	}
}

// ImportState takes the workspace id, e.g. `ws_3`.
func (r *defaultRouteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !publicIDPattern("ws").MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("expected a workspace id such as ws_3, got %q. The routeur_workspace data source resolves a slug to its id.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), req.ID)...)
}

var defaultRouteErrorAliases = map[string]string{"action.quality_bias": "quality_bias", "action": "quality_bias"}
