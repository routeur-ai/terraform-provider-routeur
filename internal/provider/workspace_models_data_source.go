package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

func init() { registerDataSource(NewWorkspaceModelsDataSource) }

var _ datasource.DataSourceWithConfigure = (*workspaceModelsDataSource)(nil)

type workspaceModelsDataSource struct{ dataSourceBase }

// NewWorkspaceModelsDataSource is the routeur_workspace_models data source.
func NewWorkspaceModelsDataSource() datasource.DataSource {
	return &workspaceModelsDataSource{dataSourceBase{name: "workspace_models"}}
}

type workspaceModelsModel struct {
	WorkspaceID  types.String          `tfsdk:"workspace_id"`
	Active       types.Bool            `tfsdk:"active"`
	ProviderName types.String          `tfsdk:"provider_name"`
	Models       []workspaceModelEntry `tfsdk:"models"`
}

type workspaceModelEntry struct {
	Model  types.String `tfsdk:"model"`
	Active types.Bool   `tfsdk:"active"`
	workspaceModelInfo
}

func (d *workspaceModelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists a workspace's model catalogue: the models of every provider the organisation has an enabled credential for " +
			"(curated models plus models seen by discovery), with whether the workspace routes to each. Ordered by `model`. " +
			"Use `routeur_workspace_model` to activate one.",
		Attributes: map[string]schema.Attribute{
			"workspace_id": schema.StringAttribute{
				MarkdownDescription: "Id of the workspace (`ws_…`), e.g. `routeur_workspace.example.id`.",
				Required:            true,
				Validators:          []validator.String{publicID("ws")},
			},
			"active": schema.BoolAttribute{
				MarkdownDescription: "Only models whose `active` equals this. Omit for all.",
				Optional:            true,
			},
			"provider_name": schema.StringAttribute{
				MarkdownDescription: "Only this provider's models, e.g. `openai`. Omit for all.",
				Optional:            true,
			},
			"models": schema.ListNestedAttribute{
				MarkdownDescription: "The matching models.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"model":          schema.StringAttribute{MarkdownDescription: "The model, as `<provider>:<model_id>` — the `model` argument of `routeur_workspace_model`.", Computed: true},
					"active":         schema.BoolAttribute{MarkdownDescription: "Whether the workspace routes to the model. Opt-in: false until set.", Computed: true},
					"provider_name":  schema.StringAttribute{MarkdownDescription: workspaceModelProviderDesc, Computed: true},
					"model_id":       schema.StringAttribute{MarkdownDescription: workspaceModelModelIDDesc, Computed: true},
					"display_name":   schema.StringAttribute{MarkdownDescription: workspaceModelDisplayNameDesc, Computed: true},
					"catalogued":     schema.BoolAttribute{MarkdownDescription: workspaceModelCataloguedDesc, Computed: true},
					"availability":   schema.StringAttribute{MarkdownDescription: workspaceModelAvailDesc, Computed: true},
					"capabilities":   schema.SetAttribute{MarkdownDescription: workspaceModelCapsDesc, ElementType: types.StringType, Computed: true},
					"context_tokens": schema.Int64Attribute{MarkdownDescription: workspaceModelContextDesc, Computed: true},
					"input_per_1k":   schema.Float64Attribute{MarkdownDescription: workspaceModelInputDesc, Computed: true},
					"output_per_1k":  schema.Float64Attribute{MarkdownDescription: workspaceModelOutputDesc, Computed: true},
				}},
			},
		},
	}
}

func (d *workspaceModelsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg workspaceModelsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ws := cfg.WorkspaceID.ValueString()
	all, err := client.ListAll[apiWorkspaceModel](ctx, d.client, d.workspacePath(ws, "models"))
	if err != nil {
		addAPIError(ctx, &resp.Diagnostics, req.Config.Schema, "Could not list the models of workspace "+ws, err)
		return
	}

	// The API has no filters: they apply here.
	cfg.Models = []workspaceModelEntry{}
	for _, m := range all {
		if !cfg.Active.IsNull() && m.Active != cfg.Active.ValueBool() {
			continue
		}
		if !cfg.ProviderName.IsNull() && m.Provider != cfg.ProviderName.ValueString() {
			continue
		}
		e := workspaceModelEntry{Model: types.StringValue(m.ID), Active: types.BoolValue(m.Active)}
		e.fromAPI(m)
		cfg.Models = append(cfg.Models, e)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}
