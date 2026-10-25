package provider

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewAnomalyDetectorResource) }

var (
	_ resource.ResourceWithConfigure      = (*anomalyDetectorResource)(nil)
	_ resource.ResourceWithImportState    = (*anomalyDetectorResource)(nil)
	_ resource.ResourceWithValidateConfig = (*anomalyDetectorResource)(nil)
)

// apiAnomalyThreshold is the per-type threshold bag: exactly one key is set.
type apiAnomalyThreshold struct {
	BaselinePct  *int64       `json:"baseline_pct,omitempty"`
	AmountCents  *int64       `json:"amount_cents,omitempty"`
	P95Ms        *int64       `json:"p95_ms,omitempty"`
	ErrorRatePct *json.Number `json:"error_rate_pct,omitempty"`
}

// apiAnomalyDetector is the AnomalyDetector schema of the API.
type apiAnomalyDetector struct {
	ID            string              `json:"id"`
	WorkspaceID   string              `json:"workspace_id"`
	Name          string              `json:"name"`
	Type          string              `json:"type"`
	Threshold     apiAnomalyThreshold `json:"threshold"`
	WindowMinutes int64               `json:"window_minutes"`
	Action        string              `json:"action"`
	Enabled       bool                `json:"enabled"`
	CreatedAt     string              `json:"created_at"`
	UpdatedAt     string              `json:"updated_at"`
}

type anomalyThresholdModel struct {
	BaselinePct  types.Int64   `tfsdk:"baseline_pct"`
	AmountCents  types.Int64   `tfsdk:"amount_cents"`
	P95Ms        types.Int64   `tfsdk:"p95_ms"`
	ErrorRatePct types.Float64 `tfsdk:"error_rate_pct"`
}

type anomalyDetectorModel struct {
	ID            types.String           `tfsdk:"id"`
	WorkspaceID   types.String           `tfsdk:"workspace_id"`
	Name          types.String           `tfsdk:"name"`
	Type          types.String           `tfsdk:"type"`
	Threshold     *anomalyThresholdModel `tfsdk:"threshold"`
	WindowMinutes types.Int64            `tfsdk:"window_minutes"`
	Action        types.String           `tfsdk:"action"`
	Enabled       types.Bool             `tfsdk:"enabled"`
	CreatedAt     timetypes.RFC3339      `tfsdk:"created_at"`
	UpdatedAt     timetypes.RFC3339      `tfsdk:"updated_at"`
}

func (m *anomalyDetectorModel) fromAPI(a apiAnomalyDetector) {
	m.ID = types.StringValue(a.ID)
	m.WorkspaceID = types.StringValue(a.WorkspaceID)
	m.Name = types.StringValue(a.Name)
	m.Type = types.StringValue(a.Type)
	m.Threshold = &anomalyThresholdModel{
		BaselinePct:  types.Int64PointerValue(a.Threshold.BaselinePct),
		AmountCents:  types.Int64PointerValue(a.Threshold.AmountCents),
		P95Ms:        types.Int64PointerValue(a.Threshold.P95Ms),
		ErrorRatePct: decimalFloat64Pointer(a.Threshold.ErrorRatePct),
	}
	m.WindowMinutes = types.Int64Value(a.WindowMinutes)
	m.Action = types.StringValue(a.Action)
	m.Enabled = types.BoolValue(a.Enabled)
	m.CreatedAt = rfc3339(a.CreatedAt)
	m.UpdatedAt = rfc3339(a.UpdatedAt)
}

// anomalyThresholdKey is the threshold attribute that belongs to a type.
var anomalyThresholdKey = map[string]string{
	"volume_spike":  "baseline_pct",
	"cost_spike":    "amount_cents",
	"latency_spike": "p95_ms",
	"error_burst":   "error_rate_pct",
}

// body is the full desired detector, for POST and PUT alike. The threshold
// carries only the key of the chosen type; any other key is a 422.
func (m *anomalyDetectorModel) body() map[string]any {
	threshold := map[string]any{}
	if t := m.Threshold; t != nil {
		switch m.Type.ValueString() {
		case "volume_spike":
			threshold["baseline_pct"] = t.BaselinePct.ValueInt64()
		case "cost_spike":
			threshold["amount_cents"] = t.AmountCents.ValueInt64()
		case "latency_spike":
			threshold["p95_ms"] = t.P95Ms.ValueInt64()
		case "error_burst":
			threshold["error_rate_pct"] = t.ErrorRatePct.ValueFloat64()
		}
	}
	return map[string]any{
		"name":           m.Name.ValueString(),
		"type":           m.Type.ValueString(),
		"threshold":      threshold,
		"window_minutes": m.WindowMinutes.ValueInt64(),
		"action":         m.Action.ValueString(),
		"enabled":        m.Enabled.ValueBool(),
	}
}

const (
	anomalyDescription = "An anomaly detector in a workspace. It compares traffic over a window against a threshold (volume, cost, latency or error rate) and raises an event when it is exceeded; depending on `action` it can also refuse matching requests while the event is open."
	anomalyIDDesc      = "Public id of the detector (`anom_…`)."
	anomalyNameDesc    = "Unique in the workspace, 1–80 characters, without leading or trailing whitespace."
	anomalyTypeDesc    = "What the detector watches: `volume_spike`, `cost_spike`, `latency_spike` or `error_burst`. Set the matching `threshold` argument and no other. Changing the type updates the detector in place."
	anomalyThresholdD  = "The limit that raises an event. Set exactly one argument, the one that belongs to `type`: `baseline_pct` for `volume_spike`, `amount_cents` for `cost_spike`, `p95_ms` for `latency_spike`, `error_rate_pct` for `error_burst`."
	anomalyBaselineD   = "Percent over the baseline, an integer from 1 to 10000000. Only with `type = \"volume_spike\"`."
	anomalyCentsD      = "US cents, an integer from 1 to 1000000000. Only with `type = \"cost_spike\"`."
	anomalyP95D        = "95th-percentile latency in milliseconds, an integer from 1 to 10000000. Only with `type = \"latency_spike\"`."
	anomalyErrorRateD  = "Error rate in percent, from 0.01 to 100. Only with `type = \"error_burst\"`."
	anomalyWindowD     = "Evaluation window in minutes: 5, 15, 30, 60, 180, 360, 720 or 1440."
	anomalyActionD     = "`alert` records an event; `quarantine` and `block` also refuse matching requests while the event is open (HTTP 429, `anomaly_block_active`); `throttle` is stored but not enforced."
	anomalyEnabledD    = "Whether the detector is evaluated. Defaults to `true`; removing the argument enables the detector again."
)

type anomalyDetectorResource struct{ resourceBase }

// NewAnomalyDetectorResource is the routeur_anomaly_detector resource.
func NewAnomalyDetectorResource() resource.Resource {
	return &anomalyDetectorResource{resourceBase{name: "anomaly_detector"}}
}

func (r *anomalyDetectorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: anomalyDescription,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute(anomalyIDDesc),
			"workspace_id": workspaceIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: anomalyNameDesc,
				Required:            true,
				Validators:          name(80),
			},
			"type": schema.StringAttribute{
				MarkdownDescription: anomalyTypeDesc,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("volume_spike", "cost_spike", "latency_spike", "error_burst")},
			},
			"threshold": schema.SingleNestedAttribute{
				MarkdownDescription: anomalyThresholdD,
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"baseline_pct": schema.Int64Attribute{
						MarkdownDescription: anomalyBaselineD,
						Optional:            true,
						Validators:          []validator.Int64{int64validator.Between(1, 10000000)},
					},
					"amount_cents": schema.Int64Attribute{
						MarkdownDescription: anomalyCentsD,
						Optional:            true,
						Validators:          []validator.Int64{int64validator.Between(1, 1000000000)},
					},
					"p95_ms": schema.Int64Attribute{
						MarkdownDescription: anomalyP95D,
						Optional:            true,
						Validators:          []validator.Int64{int64validator.Between(1, 10000000)},
					},
					"error_rate_pct": schema.Float64Attribute{
						MarkdownDescription: anomalyErrorRateD,
						Optional:            true,
						Validators:          []validator.Float64{float64validator.Between(0.01, 100)},
					},
				},
			},
			"window_minutes": schema.Int64Attribute{
				MarkdownDescription: anomalyWindowD,
				Required:            true,
				Validators:          []validator.Int64{int64validator.OneOf(5, 15, 30, 60, 180, 360, 720, 1440)},
			},
			"action": schema.StringAttribute{
				MarkdownDescription: anomalyActionD,
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("alert", "throttle", "quarantine", "block")},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: anomalyEnabledD,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

// ValidateConfig requires the threshold argument of the chosen type and
// refuses the others, so a wrong combination fails at plan time.
func (r *anomalyDetectorResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var typ types.String
	var t *anomalyThresholdModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("type"), &typ)...)
	var raw types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("threshold"), &raw)...)
	if resp.Diagnostics.HasError() || typ.IsNull() || typ.IsUnknown() || raw.IsNull() || raw.IsUnknown() {
		return
	}
	t = &anomalyThresholdModel{}
	resp.Diagnostics.Append(raw.As(ctx, t, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	set := map[string]struct{ null, unknown bool }{
		"baseline_pct":   {t.BaselinePct.IsNull(), t.BaselinePct.IsUnknown()},
		"amount_cents":   {t.AmountCents.IsNull(), t.AmountCents.IsUnknown()},
		"p95_ms":         {t.P95Ms.IsNull(), t.P95Ms.IsUnknown()},
		"error_rate_pct": {t.ErrorRatePct.IsNull(), t.ErrorRatePct.IsUnknown()},
	}
	want := anomalyThresholdKey[typ.ValueString()]
	for key, s := range set {
		if s.unknown {
			continue // known only at apply (from another resource): the API checks it then
		}
		p := path.Root("threshold").AtName(key)
		switch {
		case key == want && s.null:
			resp.Diagnostics.AddAttributeError(p, "Missing threshold for type",
				"`threshold."+key+"` is required when `type` is \""+typ.ValueString()+"\".")
		case key != want && !s.null:
			resp.Diagnostics.AddAttributeError(p, "Threshold does not match type",
				"`threshold."+key+"` does not apply to `type` \""+typ.ValueString()+"\"; use `threshold."+want+"`.")
		}
	}
}

func (r *anomalyDetectorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan anomalyDetectorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	a, err := client.Send[apiAnomalyDetector](ctx, r.client, http.MethodPost, r.workspacePath(plan.WorkspaceID.ValueString(), "anomaly-detectors"), plan.body())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not create anomaly detector", err)
		return
	}
	plan.fromAPI(a)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *anomalyDetectorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state anomalyDetectorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	a, err := client.Get[apiAnomalyDetector](ctx, r.client, r.workspacePath(state.WorkspaceID.ValueString(), "anomaly-detectors", state.ID.ValueString()))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read anomaly detector", err)
		return
	}
	state.fromAPI(a)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *anomalyDetectorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state anomalyDetectorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	a, err := client.Send[apiAnomalyDetector](ctx, r.client, http.MethodPut, r.workspacePath(state.WorkspaceID.ValueString(), "anomaly-detectors", state.ID.ValueString()), plan.body())
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update anomaly detector", err)
		return
	}
	plan.fromAPI(a)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *anomalyDetectorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state anomalyDetectorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Do(ctx, http.MethodDelete, r.workspacePath(state.WorkspaceID.ValueString(), "anomaly-detectors", state.ID.ValueString()), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not delete anomaly detector "+state.Name.ValueString(), err)
	}
}

// ImportState takes `<workspace_id>/<id>`, e.g. `ws_3/anom_12`.
func (r *anomalyDetectorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req, resp, "anom")
}
