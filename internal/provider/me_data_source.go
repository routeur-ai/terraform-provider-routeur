package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerDataSource(NewMeDataSource) }

var _ datasource.DataSourceWithConfigure = (*meDataSource)(nil)

// apiMe is the Me schema of the API.
type apiMe struct {
	User struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"user"`
	Organisation struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"organisation"`
	Token struct {
		Name        string  `json:"name"`
		Fingerprint string  `json:"fingerprint"`
		ExpiresAt   *string `json:"expires_at"`
	} `json:"token"`
}

type meModel struct {
	User         *meUserModel         `tfsdk:"user"`
	Organisation *meOrganisationModel `tfsdk:"organisation"`
	Token        *meTokenModel        `tfsdk:"token"`
}

type meUserModel struct {
	ID    types.String `tfsdk:"id"`
	Email types.String `tfsdk:"email"`
	Name  types.String `tfsdk:"name"`
}

type meOrganisationModel struct {
	Slug types.String `tfsdk:"slug"`
	Name types.String `tfsdk:"name"`
}

type meTokenModel struct {
	Name        types.String      `tfsdk:"name"`
	Fingerprint types.String      `tfsdk:"fingerprint"`
	ExpiresAt   timetypes.RFC3339 `tfsdk:"expires_at"`
}

type meDataSource struct{ dataSourceBase }

// NewMeDataSource is the routeur_me data source.
func NewMeDataSource() datasource.DataSource {
	return &meDataSource{dataSourceBase{name: "me"}}
}

func (d *meDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Who the provider's API token acts as, in which organisation. Useful to check credentials or to read the organisation slug.",
		Attributes: map[string]schema.Attribute{
			"user": schema.SingleNestedAttribute{
				MarkdownDescription: "The user who minted the token; the token acts with their permissions.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"id":    schema.StringAttribute{MarkdownDescription: "Public id of the user (`user_…`).", Computed: true},
					"email": schema.StringAttribute{MarkdownDescription: "The user's email address.", Computed: true},
					"name":  schema.StringAttribute{MarkdownDescription: "The user's name.", Computed: true},
				},
			},
			"organisation": schema.SingleNestedAttribute{
				MarkdownDescription: "The organisation the token belongs to.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"slug": schema.StringAttribute{MarkdownDescription: "The organisation slug: the provider's `org` argument.", Computed: true},
					"name": schema.StringAttribute{MarkdownDescription: "The organisation's display name.", Computed: true},
				},
			},
			"token": schema.SingleNestedAttribute{
				MarkdownDescription: "The API token itself (never its secret).",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"name":        schema.StringAttribute{MarkdownDescription: "The name given to the token when it was minted.", Computed: true},
					"fingerprint": schema.StringAttribute{MarkdownDescription: "Non-secret fingerprint of the token, e.g. `rtm_…abcd`.", Computed: true},
					"expires_at":  schema.StringAttribute{CustomType: timetypes.RFC3339Type{}, MarkdownDescription: "When the token expires (RFC 3339, UTC); null when it does not.", Computed: true},
				},
			},
		},
	}
}

func (d *meDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	me, err := client.Get[apiMe](ctx, d.client, "/me")
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Config.Schema, "Could not read the token's identity (GET /me)", err)
		return
	}
	state := meModel{
		User: &meUserModel{
			ID:    types.StringValue(me.User.ID),
			Email: types.StringValue(me.User.Email),
			Name:  types.StringValue(me.User.Name),
		},
		Organisation: &meOrganisationModel{
			Slug: types.StringValue(me.Organisation.Slug),
			Name: types.StringValue(me.Organisation.Name),
		},
		Token: &meTokenModel{
			Name:        types.StringValue(me.Token.Name),
			Fingerprint: types.StringValue(me.Token.Fingerprint),
			ExpiresAt:   rfc3339Pointer(me.Token.ExpiresAt),
		},
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
