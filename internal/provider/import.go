package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// splitImportID splits an import id into exactly len(parts) non-empty
// pieces on "/". The last piece takes the rest, so a model slug such as
// `ws_3/google:models/gemini-pro` splits into two. parts name the pieces for
// the error message.
func splitImportID(id string, parts ...string) ([]string, error) {
	pieces := strings.SplitN(id, "/", len(parts))
	if len(pieces) != len(parts) {
		return nil, fmt.Errorf("expected an import id of the form %s, got %q", "<"+strings.Join(parts, ">/<")+">", id)
	}
	for i, p := range pieces {
		if p == "" {
			return nil, fmt.Errorf("expected an import id of the form %s, got %q (empty %s)", "<"+strings.Join(parts, ">/<")+">", id, parts[i])
		}
	}
	return pieces, nil
}

// importWorkspaceChild implements ImportState for a workspace-nested
// resource imported as `<workspace_id>/<id>`: it sets workspace_id and id,
// then Read fills in the rest.
//
//	func (r *projectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
//		importWorkspaceChild(ctx, req, resp, "proj")
//	}
func importWorkspaceChild(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, idPrefix string) {
	pieces, err := splitImportID(req.ID, "workspace_id", "id")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	if !publicIDPattern("ws").MatchString(pieces[0]) {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("workspace_id must be a workspace id such as ws_3, got %q. The routeur_workspace data source resolves a slug to its id.", pieces[0]))
		return
	}
	if idPrefix != "" && !publicIDPattern(idPrefix).MatchString(pieces[1]) {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("id must look like %s_12, got %q.", idPrefix, pieces[1]))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), pieces[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), pieces[1])...)
}
