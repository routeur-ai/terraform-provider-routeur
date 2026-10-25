package provider

import (
	"context"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewAPIKeyResource) }

var (
	_ resource.ResourceWithConfigure   = (*apiKeyResource)(nil)
	_ resource.ResourceWithImportState = (*apiKeyResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*apiKeyResource)(nil)
)

// apiKeyStateRevoked is the one ApiKey.state the provider acts on: a revoked
// key can never be used or re-enabled again, so it is replaced.
const apiKeyStateRevoked = "revoked"

// apiAPIKey is the ApiKey schema of the API; Key (the secret) is set only
// by create (ApiKeyWithSecret). last_used_at is deliberately not decoded.
type apiAPIKey struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"project_id"`
	Name        string  `json:"name"`
	Fingerprint string  `json:"fingerprint"`
	State       string  `json:"state"`
	Enabled     bool    `json:"enabled"`
	RevokedAt   *string `json:"revoked_at"`
	DisabledAt  *string `json:"disabled_at"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	Key         *string `json:"key"`
}

type apiKeyModel struct {
	ID               types.String      `tfsdk:"id"`
	WorkspaceID      types.String      `tfsdk:"workspace_id"`
	ProjectID        types.String      `tfsdk:"project_id"`
	Name             types.String      `tfsdk:"name"`
	Enabled          types.Bool        `tfsdk:"enabled"`
	RotationTriggers types.Map         `tfsdk:"rotation_triggers"`
	Secret           types.String      `tfsdk:"secret"`
	Fingerprint      types.String      `tfsdk:"fingerprint"`
	State            types.String      `tfsdk:"state"`
	RevokedAt        timetypes.RFC3339 `tfsdk:"revoked_at"`
	DisabledAt       timetypes.RFC3339 `tfsdk:"disabled_at"`
	CreatedAt        timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt        timetypes.RFC3339 `tfsdk:"updated_at"`
}

// fromAPI copies the response into m. The secret is only ever in the create
// response: any other response leaves the stored secret alone.
func (m *apiKeyModel) fromAPI(k apiAPIKey) {
	m.ID = types.StringValue(k.ID)
	m.ProjectID = types.StringValue(k.ProjectID)
	m.Name = types.StringValue(k.Name)
	m.Enabled = types.BoolValue(k.Enabled)
	m.Fingerprint = types.StringValue(k.Fingerprint)
	m.State = types.StringValue(k.State)
	m.RevokedAt = rfc3339Pointer(k.RevokedAt)
	m.DisabledAt = rfc3339Pointer(k.DisabledAt)
	m.CreatedAt = rfc3339(k.CreatedAt)
	m.UpdatedAt = rfc3339(k.UpdatedAt)
	if k.Key != nil {
		m.Secret = types.StringValue(*k.Key)
	}
}

const (
	apiKeyDescription = "A gateway API key (`sk-r-…`) for `api.routeur.ai`, in one project of a workspace. " +
		"The secret is returned only when the key is created, so it is kept in state: protect your state accordingly. " +
		"Destroying the resource revokes and deletes the key; the gateway refuses it once the change propagates."
	apiKeyIDDescription        = "Public id of the API key (`key_…`). A new key, after rotation or replacement, has a new id."
	apiKeyProjectIDDescription = "Id of the project the key belongs to (`proj_…`), in the same workspace. " +
		"Omitted at creation, the key goes to the workspace's default project (removing the argument later leaves the key where it is). " +
		"Changing it moves the key in place; the secret is unchanged."
	apiKeyNameDescription             = "Display name, 1–80 characters, without leading or trailing whitespace. Renaming keeps the secret."
	apiKeyEnabledDescription          = "Whether the gateway accepts the key. Defaults to `true`; `false` disables it reversibly (the secret is kept), and removing the argument enables it again."
	apiKeyRotationTriggersDescription = "Arbitrary map of values that, when changed, rotate the key: Terraform replaces it with a new key (new id and secret) and deletes the old one. " +
		"Add `lifecycle { create_before_destroy = true }` to create the new key before the old one stops working. " +
		"Caveat: Terraform applies `create_before_destroy` to everything the key depends on as well, so replacing the key's workspace or project then creates the new one first — " +
		"which fails while the old one still holds its slug (workspace slugs are unique per organisation, and so are project slugs). Change such a slug in its own apply, or drop `create_before_destroy` for that apply. " +
		"After `terraform import` (when no triggers are stored yet), adding `rotation_triggers` stores them without replacing the key; only a later change rotates it."
	apiKeySecretDescription = "The key itself (`sk-r-…`), to send to `api.routeur.ai`. Returned by the API only at creation, so it is null after `terraform import`. Sensitive."
	apiKeyFingerprintDesc   = "Display form of the key, e.g. `sk-r-…abcd`. Safe to show; never reveals the secret."
	apiKeyStateDescription  = "`active`, `disabled` or `revoked`; new states may be added. A key revoked outside Terraform cannot be used again, so the next plan replaces it."
	apiKeyRevokedAtDesc     = "When the key was revoked (RFC 3339, UTC); null unless revoked."
	apiKeyDisabledAtDesc    = "When the key was disabled (RFC 3339, UTC); null unless disabled."
)

type apiKeyResource struct{ resourceBase }

// NewAPIKeyResource is the routeur_api_key resource.
func NewAPIKeyResource() resource.Resource {
	return &apiKeyResource{resourceBase{name: "api_key"}}
}

func (r *apiKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: apiKeyDescription,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute(apiKeyIDDescription),
			"workspace_id": workspaceIDAttribute(),
			"project_id": schema.StringAttribute{
				MarkdownDescription: apiKeyProjectIDDescription,
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{publicID("proj")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: apiKeyNameDescription,
				Required:            true,
				Validators:          name(80),
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: apiKeyEnabledDescription,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"rotation_triggers": schema.MapAttribute{
				MarkdownDescription: apiKeyRotationTriggersDescription,
				ElementType:         types.StringType,
				Optional:            true,
				PlanModifiers:       []planmodifier.Map{rotationTriggersReplace()},
			},
			"secret": schema.StringAttribute{
				MarkdownDescription: apiKeySecretDescription,
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fingerprint": schema.StringAttribute{
				MarkdownDescription: apiKeyFingerprintDesc,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"state": schema.StringAttribute{
				MarkdownDescription: apiKeyStateDescription,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, // unknown when enabled changes: ModifyPlan
			},
			"revoked_at": schema.StringAttribute{
				MarkdownDescription: apiKeyRevokedAtDesc,
				CustomType:          timetypes.RFC3339Type{},
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, // unknown when enabled changes: ModifyPlan
			},
			"disabled_at": schema.StringAttribute{
				MarkdownDescription: apiKeyDisabledAtDesc,
				CustomType:          timetypes.RFC3339Type{},
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, // unknown when enabled changes: ModifyPlan
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

// ModifyPlan replaces a key that was revoked outside Terraform, since it can
// never be used or changed again. Otherwise state, disabled_at and
// revoked_at keep their planned values unless enabled changes.
func (r *apiKeyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return // create or destroy
	}
	var state types.String
	var planEnabled, stateEnabled types.Bool
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("state"), &state)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("enabled"), &stateEnabled)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("enabled"), &planEnabled)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.ValueString() == apiKeyStateRevoked {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("state"), types.StringUnknown())...)
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("state"))
		return
	}
	if !planEnabled.Equal(stateEnabled) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("state"), types.StringUnknown())...)
		for _, a := range []string{"disabled_at", "revoked_at"} {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(a), timetypes.NewRFC3339Unknown())...)
		}
	}
}

// rotationTriggersReplace replaces the key when rotation_triggers changes
// from a stored value. A null stored value (a key just imported) is updated
// in place, so adopting a live key never revokes it.
func rotationTriggersReplace() planmodifier.Map {
	return mapplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.MapRequest, resp *mapplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !req.StateValue.IsNull()
		},
		"A change replaces the key, unless the stored value is null (just imported), when the triggers are stored in place.",
		"A change replaces the key, unless the stored value is null (just imported), when the triggers are stored in place.",
	)
}

func (r *apiKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := map[string]any{
		"name":    plan.Name.ValueString(),
		"enabled": plan.Enabled.ValueBool(),
	}
	if !plan.ProjectID.IsNull() && !plan.ProjectID.IsUnknown() {
		body["project_id"] = plan.ProjectID.ValueString()
	}
	k, err := client.Send[apiAPIKey](ctx, r.client, http.MethodPost, r.workspacePath(plan.WorkspaceID.ValueString(), "api-keys"), body)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not create API key", err)
		return
	}
	plan.fromAPI(k)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *apiKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	k, err := client.Get[apiAPIKey](ctx, r.client, r.workspacePath(state.WorkspaceID.ValueString(), "api-keys", state.ID.ValueString()))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read API key", err)
		return
	}
	state.fromAPI(k) // keeps the secret: GET never returns it
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *apiKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// PATCH: only what changed. None of these fields is nullable; an omitted
	// project_id is planned from state, so it never reads as a change.
	body := map[string]any{}
	if !plan.Name.Equal(state.Name) {
		body["name"] = plan.Name.ValueString()
	}
	if !plan.ProjectID.IsUnknown() && !plan.ProjectID.IsNull() && !plan.ProjectID.Equal(state.ProjectID) {
		body["project_id"] = plan.ProjectID.ValueString()
	}
	if !plan.Enabled.Equal(state.Enabled) {
		body["enabled"] = plan.Enabled.ValueBool()
	}

	// Only rotation_triggers changed (stored in place after an import): no
	// write, just refresh the computed values.
	keyPath := r.workspacePath(state.WorkspaceID.ValueString(), "api-keys", state.ID.ValueString())
	var k apiAPIKey
	var err error
	if len(body) == 0 {
		k, err = client.Get[apiAPIKey](ctx, r.client, keyPath)
	} else {
		k, err = client.Send[apiAPIKey](ctx, r.client, http.MethodPatch, keyPath, body)
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update API key", err)
		return
	}
	plan.Secret = state.Secret
	plan.fromAPI(k)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *apiKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// DELETE revokes and deletes in one step, whatever the key's state.
	err := r.client.Do(ctx, http.MethodDelete, r.workspacePath(state.WorkspaceID.ValueString(), "api-keys", state.ID.ValueString()), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not delete API key "+state.ID.ValueString(), err)
	}
}

// ImportState takes `<workspace_id>/<id>`, e.g. `ws_3/key_12`. The secret
// cannot be read back, so it is null after import.
func (r *apiKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req, resp, "key")
}
