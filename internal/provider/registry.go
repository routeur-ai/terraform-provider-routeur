package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// Each resource and data source file registers itself from init(), so
// adding one never edits a shared file:
//
//	func init() { registerResource(NewWorkspaceResource) }
var (
	resourceFactories   []func() resource.Resource
	dataSourceFactories []func() datasource.DataSource
)

// registerResource and registerDataSource also wrap each one so the API's
// deprecation headers become warnings (notices.go).
func registerResource(f func() resource.Resource) {
	resourceFactories = append(resourceFactories, func() resource.Resource { return withNotices(f()) })
}

func registerDataSource(f func() datasource.DataSource) {
	dataSourceFactories = append(dataSourceFactories, func() datasource.DataSource { return withDataSourceNotices(f()) })
}

// providerData is what Configure hands to every resource and data source.
type providerData struct {
	client *client.Client
	org    string
}

// orgPath builds a path under /orgs/{org}, escaping every segment.
func (d *providerData) orgPath(segments ...string) string {
	return client.Path(append([]string{"orgs", d.org}, segments...)...)
}

// workspacePath builds a path under /orgs/{org}/workspaces/{workspaceID}.
func (d *providerData) workspacePath(workspaceID string, segments ...string) string {
	return d.orgPath(append([]string{"workspaces", workspaceID}, segments...)...)
}

// resourceBase gives a resource its type name and provider data. Embed it
// and set name to the part after "routeur_":
//
//	type workspaceResource struct{ resourceBase }
//	func NewWorkspaceResource() resource.Resource {
//		return &workspaceResource{resourceBase{name: "workspace"}}
//	}
type resourceBase struct {
	name string
	*providerData
}

func (r *resourceBase) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.name
}

func (r *resourceBase) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return // validate, or a plan before the provider is configured
	}
	data, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerData, got %T. Please report this issue to the provider developers.", req.ProviderData))
		return
	}
	r.providerData = data
}

// dataSourceBase is resourceBase for data sources.
type dataSourceBase struct {
	name string
	*providerData
}

func (d *dataSourceBase) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.name
}

func (d *dataSourceBase) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerData, got %T. Please report this issue to the provider developers.", req.ProviderData))
		return
	}
	d.providerData = data
}
