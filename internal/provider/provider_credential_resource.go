package provider

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerResource(NewProviderCredentialResource) }

var (
	_ resource.ResourceWithConfigure        = (*providerCredentialResource)(nil)
	_ resource.ResourceWithImportState      = (*providerCredentialResource)(nil)
	_ resource.ResourceWithModifyPlan       = (*providerCredentialResource)(nil)
	_ resource.ResourceWithConfigValidators = (*providerCredentialResource)(nil)
)

// knownProviders are the upstream provider slugs the API accepted when this
// version was released. The list grows with routeur.ai, so another value is
// a warning, not an error (growingEnum), and any value is read back.
var knownProviders = []string{"openai", "anthropic", "google", "deepseek", "mistral"}

// providerNameAlias maps the API's `provider` field onto provider_name
// ("provider" is reserved in Terraform).
var providerNameAlias = map[string]string{"provider": "provider_name"}

// apiProviderCredential is the ProviderCredential schema of the API. The key
// is write-only and never in a response.
type apiProviderCredential struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	Label       string `json:"label"`
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type providerCredentialModel struct {
	ID           types.String      `tfsdk:"id"`
	ProviderName types.String      `tfsdk:"provider_name"`
	Label        types.String      `tfsdk:"label"`
	Key          types.String      `tfsdk:"key"`
	KeyWO        types.String      `tfsdk:"key_wo"`
	KeyWOVersion types.Int64       `tfsdk:"key_wo_version"`
	Enabled      types.Bool        `tfsdk:"enabled"`
	Fingerprint  types.String      `tfsdk:"fingerprint"`
	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
}

func (m *providerCredentialModel) fromAPI(c apiProviderCredential) {
	m.ID = types.StringValue(c.ID)
	m.ProviderName = types.StringValue(c.Provider)
	m.Label = types.StringValue(c.Label)
	m.Enabled = types.BoolValue(c.Enabled)
	m.Fingerprint = types.StringValue(c.Fingerprint)
	m.CreatedAt = rfc3339(c.CreatedAt)
	m.UpdatedAt = rfc3339(c.UpdatedAt)
	m.KeyWO = types.StringNull() // write-only: never stored
}

const (
	providerCredentialDescription = "An upstream LLM provider API key held by the organisation: the key the gateway uses for that provider's traffic. " +
		"An organisation holds at most one credential per provider. The key is verified against the provider before it is stored, sealed at rest and never returned; only its `fingerprint` is shown.\n\n" +
		"Set the key with exactly one of `key` (kept in state, marked sensitive) or `key_wo` + `key_wo_version` (write-only: never stored in the plan or state; needs Terraform 1.11 or later). " +
		"If the key is changed outside Terraform, the changed `fingerprint` makes the next plan send the configured key again."
	providerCredentialIDDescription = "Public id of the credential (`cred_…`)."
	providerNameDescription         = "Upstream provider, e.g. `openai`, `anthropic`, `google`, `deepseek` or `mistral`. " +
		"Providers routeur.ai adds later work too: a value this provider version does not know is a warning, and the API decides. " +
		"An organisation can hold one credential per provider. Set at create only; changing it replaces the credential. " +
		"(The API calls this field `provider`, a name Terraform reserves.)"
	providerCredentialLabelDescription = "Label, 1–60 characters without leading or trailing whitespace, e.g. `prod`."
	providerCredentialKeyDescription   = "The provider API key, 8–512 characters. Stored in state (sensitive). " +
		"Changing it rotates the credential in place: the new key is verified, and the gateway switches to it without a gap. " +
		"Conflicts with `key_wo`."
	providerCredentialKeyWODescription = "The provider API key, 8–512 characters, as a write-only argument: never stored in the plan or state (Terraform 1.11 or later). " +
		"Terraform cannot see when it changes, so bump `key_wo_version` to send a new key. Requires `key_wo_version`; conflicts with `key`."
	providerCredentialKeyWOVersionDescription = "Any number, changed to send `key_wo` again — rotating the credential in place when the key differs. Requires `key_wo`."
	providerCredentialEnabledDescription      = "Whether the gateway uses the credential. Defaults to `true`; removing the argument enables it again."
	providerCredentialFingerprintDescription  = "Display form of the stored key, e.g. `sk-proj…a91f` (`…a91f` for short keys). Changes when the key is rotated; never reveals the key."
)

type providerCredentialResource struct{ resourceBase }

// NewProviderCredentialResource is the routeur_provider_credential resource.
func NewProviderCredentialResource() resource.Resource {
	return &providerCredentialResource{resourceBase{name: "provider_credential"}}
}

// providerKeyValidators check a provider key without ever echoing it.
func providerKeyValidators() []validator.String {
	return []validator.String{stringvalidator.UTF8LengthBetween(8, 512), secretWhitespaceValidator{}}
}

func (r *providerCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: providerCredentialDescription,
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(providerCredentialIDDescription),
			"provider_name": schema.StringAttribute{
				MarkdownDescription: providerNameDescription,
				Required:            true,
				Validators:          []validator.String{growingEnum("provider", knownProviders...)},
				PlanModifiers:       createOnly(),
			},
			"label": schema.StringAttribute{
				MarkdownDescription: providerCredentialLabelDescription,
				Required:            true,
				Validators:          name(60),
			},
			"key": schema.StringAttribute{
				MarkdownDescription: providerCredentialKeyDescription,
				Optional:            true,
				Sensitive:           true,
				Validators:          providerKeyValidators(),
			},
			"key_wo": schema.StringAttribute{
				MarkdownDescription: providerCredentialKeyWODescription,
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Validators:          providerKeyValidators(),
			},
			"key_wo_version": schema.Int64Attribute{
				MarkdownDescription: providerCredentialKeyWOVersionDescription,
				Optional:            true,
				Validators:          []validator.Int64{int64validator.AlsoRequires(path.MatchRoot("key_wo"))},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: providerCredentialEnabledDescription,
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"fingerprint": schema.StringAttribute{
				MarkdownDescription: providerCredentialFingerprintDescription,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": createdAtAttribute(),
			"updated_at": updatedAtAttribute(),
		},
	}
}

func (r *providerCredentialResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("key"), path.MatchRoot("key_wo")),
		resourcevalidator.RequiredTogether(path.MatchRoot("key_wo"), path.MatchRoot("key_wo_version")),
	}
}

// keyChanged tells whether plan sends a key that state does not reflect:
// a new `key`, or a new `key_wo_version` (Read nulls either when the
// fingerprint shows the key changed outside Terraform).
func keyChanged(plan, state providerCredentialModel) bool {
	if !plan.Key.IsNull() && !plan.Key.Equal(state.Key) {
		return true
	}
	return !plan.KeyWOVersion.IsNull() && !plan.KeyWOVersion.Equal(state.KeyWOVersion)
}

// ModifyPlan marks the fingerprint unknown when the key is about to be
// sent: it is the one computed value an update can change besides
// updated_at.
func (r *providerCredentialResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state providerCredentialModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if keyChanged(plan, state) || plan.Key.IsUnknown() || plan.KeyWOVersion.IsUnknown() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("fingerprint"), types.StringUnknown())...)
	}
}

// configKey is the key to send: `key` from the plan, or the write-only
// `key_wo`, which is only ever in the configuration.
func configKey(ctx context.Context, plan providerCredentialModel, config tfsdk.Config) (string, diag.Diagnostics) {
	if !plan.Key.IsNull() {
		return plan.Key.ValueString(), nil
	}
	var wo types.String
	d := config.GetAttribute(ctx, path.Root("key_wo"), &wo)
	return wo.ValueString(), d
}

func (r *providerCredentialResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan providerCredentialModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, d := configKey(ctx, plan, req.Config)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := map[string]any{
		"provider": plan.ProviderName.ValueString(),
		"label":    plan.Label.ValueString(),
		"key":      key,
		"enabled":  plan.Enabled.ValueBool(),
	}
	c, err := client.Send[apiProviderCredential](ctx, r.client, http.MethodPost, r.orgPath("provider-credentials"), body)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not create "+plan.ProviderName.ValueString()+" credential", withDuplicateCredentialHint(err), providerNameAlias)
		return
	}
	plan.fromAPI(c)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *providerCredentialResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state providerCredentialModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.Get[apiProviderCredential](ctx, r.client, r.orgPath("provider-credentials", state.ID.ValueString()))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not read provider credential", err, providerNameAlias)
		return
	}

	// The key is write-only, so its drift shows only as a new fingerprint.
	// Forget the last sent key so the next plan sends the configured one.
	if !state.Fingerprint.IsNull() && state.Fingerprint.ValueString() != c.Fingerprint {
		state.Key = types.StringNull()
		state.KeyWOVersion = types.Int64Null()
	}
	state.fromAPI(c)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *providerCredentialResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state providerCredentialModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// PATCH: only what changed. Resending the stored key is a no-op on the
	// server.
	body := map[string]any{}
	if !plan.Label.Equal(state.Label) {
		body["label"] = plan.Label.ValueString()
	}
	if !plan.Enabled.Equal(state.Enabled) {
		body["enabled"] = plan.Enabled.ValueBool()
	}
	if keyChanged(plan, state) {
		key, d := configKey(ctx, plan, req.Config)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
		body["key"] = key
	}

	c, err := client.Send[apiProviderCredential](ctx, r.client, http.MethodPatch, r.orgPath("provider-credentials", state.ID.ValueString()), body)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Plan.Schema, "Could not update "+state.ProviderName.ValueString()+" credential", err, providerNameAlias)
		return
	}
	plan.fromAPI(c)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *providerCredentialResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state providerCredentialModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Do(ctx, http.MethodDelete, r.orgPath("provider-credentials", state.ID.ValueString()), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		addAPIError(ctx, &resp.Diagnostics, req.State.Schema, "Could not delete "+state.ProviderName.ValueString()+" credential", err, providerNameAlias)
	}
}

// ImportState takes the credential id, e.g. `cred_7`. The key cannot be read
// back, so the first apply after an import sends the configured key once
// (a no-op on the server when it matches).
func (r *providerCredentialResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !publicIDPattern("cred").MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("expected a credential id such as cred_7, got %q.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// duplicateCredentialHint follows the API's refusal of a second credential
// for a provider.
const duplicateCredentialHint = "To manage the existing credential with Terraform instead, import it: " +
	"`terraform import routeur_provider_credential.<name> cred_<N>` (or an `import` block). " +
	"Its id is shown in routeur.ai under Settings → Provider credentials, or by `GET /orgs/<org>/provider-credentials`."

// withDuplicateCredentialHint appends duplicateCredentialHint to the API's
// per-field message for a duplicate credential; any other error is returned
// unchanged.
func withDuplicateCredentialHint(err error) error {
	apiErr, ok := client.AsAPIError(err)
	if !ok {
		return err
	}
	msgs := apiErr.Fields["provider"]
	if !slices.ContainsFunc(msgs, func(m string) bool { return strings.Contains(m, "already has a credential") }) {
		return err
	}
	cp := *apiErr
	cp.Fields = maps.Clone(apiErr.Fields)
	cp.Fields["provider"] = append(slices.Clone(msgs), "\n\n"+duplicateCredentialHint)
	return &cp
}

// secretWhitespaceValidator refuses a secret that starts or ends with
// whitespace (the API refuses it with a 422) without putting the value in
// the diagnostic, unlike the generic validators.
type secretWhitespaceValidator struct{}

func (secretWhitespaceValidator) Description(context.Context) string {
	return "must not start or end with whitespace"
}

func (v secretWhitespaceValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v secretWhitespaceValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if s := req.ConfigValue.ValueString(); strings.TrimSpace(s) != s {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid value", fmt.Sprintf("%s %s (a stray newline from a pasted key, perhaps).", req.Path, v.Description(ctx)))
	}
}
