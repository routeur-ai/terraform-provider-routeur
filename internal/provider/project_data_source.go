package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerDataSource(NewProjectDataSource) }

var _ datasource.DataSourceWithConfigure = (*projectDataSource)(nil)

type projectDataSource struct{ dataSourceBase }

// NewProjectDataSource is the routeur_project data source.
func NewProjectDataSource() datasource.DataSource {
	return &projectDataSource{dataSourceBase{name: "project"}}
}

func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	oneOf := stringvalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("slug"))
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a project in a workspace by `slug` or `id` — for example a workspace's default project. " + projectDescription,
		Attributes: map[string]schema.Attribute{
			"workspace_id": schema.StringAttribute{
				MarkdownDescription: "Id of the workspace to look in (`ws_…`).",
				Required:            true,
				Validators:          []validator.String{publicID("ws")},
			},
			"id": schema.StringAttribute{
				MarkdownDescription: projectIDDescription + " Set exactly one of `id` and `slug`.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{publicID("proj"), oneOf},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "The project's slug. Set exactly one of `id` and `slug`.",
				Optional:            true,
				Computed:            true,
				Validators:          append(hyphenSlug(projectSlugMax), oneOf),
			},
			"name":        schema.StringAttribute{MarkdownDescription: projectNameDescription, Computed: true},
			"description": schema.StringAttribute{MarkdownDescription: projectDescriptionDescription, Computed: true},
			"enabled":     schema.BoolAttribute{MarkdownDescription: "Whether the project is enabled.", Computed: true},
			"is_default":  schema.BoolAttribute{MarkdownDescription: projectIsDefaultDescription, Computed: true},
			"created_at":  schema.StringAttribute{CustomType: timetypes.RFC3339Type{}, MarkdownDescription: "When it was created (RFC 3339, UTC).", Computed: true},
			"updated_at":  schema.StringAttribute{CustomType: timetypes.RFC3339Type{}, MarkdownDescription: "When it was last changed (RFC 3339, UTC).", Computed: true},
		},
	}
}

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg projectModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	workspaceID := cfg.WorkspaceID.ValueString()

	if !cfg.ID.IsNull() {
		p, err := client.Get[apiProject](ctx, d.client, d.workspacePath(workspaceID, "projects", cfg.ID.ValueString()))
		if err != nil {
			addAPIError(ctx, &resp.Diagnostics, req.Config.Schema, "Could not read project "+cfg.ID.ValueString(), err)
			return
		}
		cfg.fromAPI(p)
		resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
		return
	}

	// No get-by-slug endpoint: list the workspace's projects.
	slug := cfg.Slug.ValueString()
	projects, err := client.ListAll[apiProject](ctx, d.client, d.workspacePath(workspaceID, "projects"))
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Config.Schema, "Could not list projects in "+workspaceID, err)
		return
	}
	for _, p := range projects {
		if p.Slug == slug {
			cfg.fromAPI(p)
			resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
			return
		}
	}
	resp.Diagnostics.AddAttributeError(path.Root("slug"), "Project not found",
		fmt.Sprintf("Workspace %s has no project with slug %q.", workspaceID, slug))
}
