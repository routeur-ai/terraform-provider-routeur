// Package provider implements the routeur.ai Terraform provider.
package provider

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// Environment variables read when the matching argument is not set. Not
// ROUTEUR_KEY: that is an API key, for api.routeur.ai.
const (
	EnvToken   = "ROUTEUR_TOKEN"
	EnvOrg     = "ROUTEUR_ORG"
	EnvBaseURL = "ROUTEUR_BASE_URL"
)

var _ provider.Provider = (*routeurProvider)(nil)

type routeurProvider struct {
	version string
}

type providerModel struct {
	Token   types.String `tfsdk:"token"`
	Org     types.String `tfsdk:"org"`
	BaseURL types.String `tfsdk:"base_url"`
}

// New returns a provider factory for providerserver.Serve and tests.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &routeurProvider{version: version} }
}

func (p *routeurProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "routeur"
	resp.Version = p.version
}

func (p *routeurProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage [routeur.ai](https://routeur.ai) configuration as code — workspaces, projects, API keys, " +
			"routing rules, provider credentials and policies — through the management API at `app.routeur.ai/api/v1`. " +
			"The provider manages configuration only; AI traffic goes to `api.routeur.ai`.",
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				MarkdownDescription: "A routeur.ai API token (`rtm_…`), created in [app.routeur.ai](https://app.routeur.ai) under **Settings → API tokens**. " +
					"It acts as the user who minted it, in their organisation, with their permissions. " +
					"Prefer the `" + EnvToken + "` environment variable, which keeps it out of configuration; it is used when this argument is unset. " +
					"This is not an API key: API keys (`ROUTEUR_KEY`) are what your applications use to call models through `api.routeur.ai`.",
				Optional:  true,
				Sensitive: true,
			},
			"org": schema.StringAttribute{
				MarkdownDescription: "Slug of the organisation to manage. May also be set with the `" + EnvOrg + "` " +
					"environment variable. Defaults to the token's own organisation. An API token only works in its own " +
					"organisation, so the provider checks this against `GET /me` when it starts and fails if they differ " +
					"(rather than finding nothing and planning to recreate everything). " +
					"To manage several organisations, use one provider alias, with its own token, per organisation.",
				Optional: true,
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "Base URL of the management API. May also be set with the `" + EnvBaseURL + "` " +
					"environment variable. Defaults to `" + client.DefaultBaseURL + "`.",
				Optional: true,
			},
		},
	}
}

func (p *routeurProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for name, v := range map[string]types.String{"token": cfg.Token, "org": cfg.Org, "base_url": cfg.BaseURL} {
		if v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown routeur.ai provider "+name,
				"The provider cannot be configured with a value that is only known after apply. "+
					"Set it statically, or use the matching environment variable.")
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	token := valueOrEnv(cfg.Token, EnvToken)
	org := valueOrEnv(cfg.Org, EnvOrg)
	baseURL := valueOrEnv(cfg.BaseURL, EnvBaseURL)
	if baseURL == "" {
		baseURL = client.DefaultBaseURL
	}

	if token == "" {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Missing routeur.ai API token",
			"Set the provider's token argument or the "+EnvToken+" environment variable to an API token (rtm_…), "+
				"minted in routeur.ai under Settings → API tokens.")
		return
	}
	if u, err := url.Parse(baseURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		resp.Diagnostics.AddAttributeError(path.Root("base_url"), "Invalid routeur.ai base URL",
			"base_url must be an absolute http(s) URL such as "+client.DefaultBaseURL+", got "+baseURL+".")
		return
	}

	c, err := client.New(client.Config{
		BaseURL:   baseURL,
		Token:     token,
		UserAgent: "terraform-provider-routeur/" + p.version + " (+terraform " + req.TerraformVersion + ")",
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create the routeur.ai client", err.Error())
		return
	}

	// GET /me checks the token once and names its organisation. A token only
	// works in its own organisation, so a different configured org is an
	// error here rather than a 404 on every Read.
	meCtx, notices := client.WithNotices(ctx)
	me, err := client.Get[apiMe](meCtx, c, "/me")
	addNoticeWarnings(&resp.Diagnostics, notices)
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, nil, "Could not authenticate with routeur.ai (GET /me)", err)
		return
	}
	tokenOrg := me.Organisation.Slug
	if org == "" {
		org = tokenOrg
		tflog.Debug(ctx, "routeur.ai organisation from GET /me", map[string]any{"org": org})
	} else if org != tokenOrg {
		resp.Diagnostics.AddAttributeError(path.Root("org"), "API token belongs to another organisation",
			fmt.Sprintf("This token belongs to organisation %q, not %q. An API token only works in the organisation "+
				"of the user who minted it. Fix org (or %s), or use a token minted in %q; leave org unset to use the "+
				"token's own organisation.", tokenOrg, org, EnvOrg, org))
		return
	}

	data := &providerData{client: c, org: org}
	resp.DataSourceData = data
	resp.ResourceData = data
}

func (p *routeurProvider) Resources(context.Context) []func() resource.Resource {
	return resourceFactories
}

func (p *routeurProvider) DataSources(context.Context) []func() datasource.DataSource {
	return dataSourceFactories
}

func valueOrEnv(v types.String, env string) string {
	if !v.IsNull() {
		return v.ValueString()
	}
	return os.Getenv(env)
}
