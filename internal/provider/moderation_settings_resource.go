package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewModerationSettingsResource) }

var (
	_ resource.ResourceWithConfigure   = (*moderationSettingsResource)(nil)
	_ resource.ResourceWithImportState = (*moderationSettingsResource)(nil)
)

// moderationSettingsDefault is what the API reads as "unset"; Delete resets
// to it.
const moderationSettingsDefault = "buffered"

// apiModerationSettings is the ModerationSettings schema of the API.
type apiModerationSettings struct {
	StreamingModeration string `json:"streaming_moderation"`
}

type moderationSettingsModel struct {
	ID                  types.String `tfsdk:"id"`
	StreamingModeration types.String `tfsdk:"streaming_moderation"`
}

const (
	moderationSettingsDescription = "The organisation-wide output-moderation settings: a singleton that always exists, so creating it takes it over and destroying it resets it to the routeur.ai default (`buffered`). Import it with the organisation slug. Requires a routeur.ai plan with output moderation to change."
	moderationSettingsIDDesc      = "The organisation slug."
	moderationSettingsModeDesc    = "How output moderation applies to streamed responses across the organisation: `buffered` scans the whole response before sending a byte and never leaks blocked content; `chunked` scans as tokens stream, which lowers latency but may disclose content before a block. A rule's own `streaming` overrides it."
)

type moderationSettingsResource struct{ resourceBase }

// NewModerationSettingsResource is the routeur_moderation_settings resource.
func NewModerationSettingsResource() resource.Resource {
	return &moderationSettingsResource{resourceBase{name: "moderation_settings"}}
}

func (r *moderationSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: moderationSettingsDescription,
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(moderationSettingsIDDesc),
			"streaming_moderation": schema.StringAttribute{
				MarkdownDescription: moderationSettingsModeDesc,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("buffered", "chunked")},
			},
		},
	}
}

// put replaces the settings and returns the stored value.
func (r *moderationSettingsResource) put(ctx context.Context, mode string) (apiModerationSettings, error) {
	return client.Send[apiModerationSettings](ctx, r.client, http.MethodPut, r.orgPath("moderation-settings"), map[string]any{"streaming_moderation": mode})
}

func (r *moderationSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan moderationSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.put(ctx, plan.StreamingModeration.ValueString())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not set moderation settings", err)
		return
	}
	plan.ID = types.StringValue(r.org)
	plan.StreamingModeration = types.StringValue(s.StreamingModeration)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *moderationSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state moderationSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := client.Get[apiModerationSettings](ctx, r.client, r.orgPath("moderation-settings"))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read moderation settings", err)
		return
	}
	state.ID = types.StringValue(r.org)
	state.StreamingModeration = types.StringValue(s.StreamingModeration)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *moderationSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan moderationSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.put(ctx, plan.StreamingModeration.ValueString())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update moderation settings", err)
		return
	}
	plan.ID = types.StringValue(r.org)
	plan.StreamingModeration = types.StringValue(s.StreamingModeration)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete resets the setting to the API default. The singleton itself cannot
// be removed. A setting already at the default is left alone, so destroying
// still works after a plan downgrade removed the right to write it.
func (r *moderationSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	cur, err := client.Get[apiModerationSettings](ctx, r.client, r.orgPath("moderation-settings"))
	if client.IsNotFound(err) {
		return
	}
	if err == nil && cur.StreamingModeration == moderationSettingsDefault {
		return
	}
	if _, err := r.put(ctx, moderationSettingsDefault); err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not reset moderation settings", err)
	}
}

// ImportState takes the organisation slug, which must be the provider's org.
func (r *moderationSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if r.providerData != nil && req.ID != r.org {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("expected the organisation slug %q the provider is configured for, got %q. Use a provider alias for another organisation.", r.org, req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
