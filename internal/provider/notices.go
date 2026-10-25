package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// registerResource and registerDataSource wrap every resource and data
// source so each operation collects the API's Deprecation and Sunset headers
// (client.WithNotices) and reports each distinct one once, as a warning.

// addNoticeWarnings appends one warning per notice.
func addNoticeWarnings(diags *diag.Diagnostics, notices *client.Notices) {
	for _, n := range notices.List() {
		var b strings.Builder
		fmt.Fprintf(&b, "The routeur.ai management API reports that %s %s uses a deprecated field or endpoint", n.Method, n.Path)
		if n.Deprecation != "" && n.Deprecation != "true" && n.Deprecation != "?1" {
			fmt.Fprintf(&b, " (deprecated since %s)", n.Since())
		}
		b.WriteString(".")
		if n.Sunset != "" {
			fmt.Fprintf(&b, " It may stop working after %s.", n.Sunset)
		}
		b.WriteString(" It keeps working until then. Upgrade the routeur provider; if the latest version still shows this warning, please report it to the provider developers.")
		for _, l := range n.Links {
			b.WriteString("\n\nMore information: " + l)
		}
		diags.AddWarning("Deprecated routeur.ai API usage", b.String())
	}
}

// noticeResource wraps a resource. It forwards every optional interface the
// provider's resources implement; TestResourceWrapperForwardsInterfaces
// fails if a resource gains one it does not.
type noticeResource struct{ resource.Resource }

var (
	_ resource.ResourceWithConfigure        = noticeResource{}
	_ resource.ResourceWithImportState      = noticeResource{}
	_ resource.ResourceWithModifyPlan       = noticeResource{}
	_ resource.ResourceWithValidateConfig   = noticeResource{}
	_ resource.ResourceWithConfigValidators = noticeResource{}
)

func withNotices(r resource.Resource) resource.Resource { return noticeResource{r} }

func (w noticeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	ctx, n := client.WithNotices(ctx)
	w.Resource.Create(ctx, req, resp)
	addNoticeWarnings(&resp.Diagnostics, n)
}

func (w noticeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	ctx, n := client.WithNotices(ctx)
	w.Resource.Read(ctx, req, resp)
	addNoticeWarnings(&resp.Diagnostics, n)
}

func (w noticeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	ctx, n := client.WithNotices(ctx)
	w.Resource.Update(ctx, req, resp)
	addNoticeWarnings(&resp.Diagnostics, n)
}

func (w noticeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	ctx, n := client.WithNotices(ctx)
	w.Resource.Delete(ctx, req, resp)
	addNoticeWarnings(&resp.Diagnostics, n)
}

func (w noticeResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if r, ok := w.Resource.(resource.ResourceWithConfigure); ok {
		r.Configure(ctx, req, resp)
	}
}

func (w noticeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r, ok := w.Resource.(resource.ResourceWithImportState)
	if !ok {
		resp.Diagnostics.AddError("Resource import not implemented", "This resource does not support import. Please report this to the provider developers.")
		return
	}
	r.ImportState(ctx, req, resp) // no API call: Read, which follows, collects
}

func (w noticeResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r, ok := w.Resource.(resource.ResourceWithModifyPlan); ok {
		r.ModifyPlan(ctx, req, resp)
	}
}

func (w noticeResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	if r, ok := w.Resource.(resource.ResourceWithValidateConfig); ok {
		r.ValidateConfig(ctx, req, resp)
	}
}

func (w noticeResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	if r, ok := w.Resource.(resource.ResourceWithConfigValidators); ok {
		return r.ConfigValidators(ctx)
	}
	return nil
}

// noticeDataSource wraps a data source the same way.
type noticeDataSource struct{ datasource.DataSource }

var (
	_ datasource.DataSourceWithConfigure        = noticeDataSource{}
	_ datasource.DataSourceWithValidateConfig   = noticeDataSource{}
	_ datasource.DataSourceWithConfigValidators = noticeDataSource{}
)

func withDataSourceNotices(d datasource.DataSource) datasource.DataSource {
	return noticeDataSource{d}
}

func (w noticeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx, n := client.WithNotices(ctx)
	w.DataSource.Read(ctx, req, resp)
	addNoticeWarnings(&resp.Diagnostics, n)
}

func (w noticeDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d, ok := w.DataSource.(datasource.DataSourceWithConfigure); ok {
		d.Configure(ctx, req, resp)
	}
}

func (w noticeDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	if d, ok := w.DataSource.(datasource.DataSourceWithValidateConfig); ok {
		d.ValidateConfig(ctx, req, resp)
	}
}

func (w noticeDataSource) ConfigValidators(ctx context.Context) []datasource.ConfigValidator {
	if d, ok := w.DataSource.(datasource.DataSourceWithConfigValidators); ok {
		return d.ConfigValidators(ctx)
	}
	return nil
}
