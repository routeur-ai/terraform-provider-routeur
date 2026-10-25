package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerDataSource(NewWorkspaceDataSource) }

var _ datasource.DataSourceWithConfigure = (*workspaceDataSource)(nil)

type workspaceDataSource struct{ dataSourceBase }

// NewWorkspaceDataSource is the routeur_workspace data source.
func NewWorkspaceDataSource() datasource.DataSource {
	return &workspaceDataSource{dataSourceBase{name: "workspace"}}
}

func (d *workspaceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	oneOf := stringvalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("slug"))
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a workspace by `slug` or `id` — for example to pass an existing workspace's id as `workspace_id`. " + workspaceDescription,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: workspaceIDDescription + " Set exactly one of `id` and `slug`.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{publicID("ws"), oneOf},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "The workspace's slug, unique in the organisation. Set exactly one of `id` and `slug`.",
				Optional:            true,
				Computed:            true,
				Validators:          append(hyphenSlug(projectSlugMax), oneOf), // existing workspace slugs may be longer than workspaceSlugMax
			},
			"name":               schema.StringAttribute{MarkdownDescription: workspaceNameDescription, Computed: true},
			"description":        schema.StringAttribute{MarkdownDescription: workspaceDescriptionDescription, Computed: true},
			"default_project_id": schema.StringAttribute{MarkdownDescription: workspaceDefaultProjectDesc, Computed: true},
			"is_default":         schema.BoolAttribute{MarkdownDescription: workspaceIsDefaultDescription, Computed: true},
			"created_at":         schema.StringAttribute{CustomType: timetypes.RFC3339Type{}, MarkdownDescription: "When it was created (RFC 3339, UTC).", Computed: true},
			"updated_at":         schema.StringAttribute{CustomType: timetypes.RFC3339Type{}, MarkdownDescription: "When it was last changed (RFC 3339, UTC).", Computed: true},
		},
	}
}

func (d *workspaceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg workspaceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The {workspace} path segment takes the slug or the id.
	ref := cfg.ID.ValueString()
	if ref == "" {
		ref = cfg.Slug.ValueString()
	}
	ws, err := client.Get[apiWorkspace](ctx, d.client, d.workspacePath(ref))
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Config.Schema, "Could not read workspace "+ref, err)
		return
	}
	cfg.fromAPI(ws)
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}
