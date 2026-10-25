package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// schemaPaths is satisfied by every framework schema (req.Plan.Schema,
// req.State.Schema, resource/datasource schema.Schema): it tells whether an
// attribute path exists.
type schemaPaths interface {
	TypeAtPath(context.Context, path.Path) (attr.Type, diag.Diagnostics)
}

// codeHints add a next step to API error codes whose message alone does not
// say what to do in Terraform.
var codeHints = map[string]string{
	"workspace_not_empty": "Delete, or remove from configuration, every project, API key, rule, policy, detector and budget in the workspace first. In one configuration, referencing the workspace's id from those resources makes Terraform destroy them first.",
	"project_not_empty": "Delete the API keys, rules, policies and budgets that belong to or are scoped to this project first; in one configuration, referencing the project's id from them makes Terraform destroy them first. " +
		"Moving API keys out of the project does not help in the same apply: Terraform destroys the project before it updates the keys, so this error repeats on every apply. " +
		"Do it in two applies: first move the keys (change their project_id) and apply, then remove the project and apply again.",
	"default_project_protected":   "A workspace's default project cannot be disabled or deleted; it is removed together with its workspace. Use `terraform state rm` to stop managing it.",
	"default_workspace_protected": "The organisation's default workspace cannot be deleted. Use `terraform state rm` to stop managing it.",
	"last_workspace_protected":    "An organisation must keep at least one workspace. Create another workspace first, or use `terraform state rm` to stop managing this one.",
	"default_route_protected":     "The Default route cannot be deleted or disabled; only its action can change.",
	"immutable_field":             "This field cannot change after creation; the provider should have planned a replacement. Please report this to the provider developers.",
	"mfa_required":                "The organisation requires MFA and the token's owner has not enrolled. Enrol in routeur.ai and the token works again.",
	"unauthenticated":             "Check that the API token is correct, not revoked and not expired.",
	"forbidden":                   "The token's user lacks the permission or platform seat for this operation.",
	"org_mismatch":                "The request named an organisation other than the token's. An API token only works in the organisation of the user who minted it: check the provider's org argument (or ROUTEUR_ORG), or use a token minted in that organisation. This never means the resource is missing.",
	"model_in_use":                "A routing rule in this workspace pins the model or lists it as a failover target (the message names the rules). Point those rules at another model, or remove them, before deactivating it; in one configuration, building the rules' provider and model from this resource's attributes makes Terraform update or destroy them first.",
	"rate_limited":                "The provider already retried for up to 5 minutes; lower terraform's -parallelism or try again later.",
}

// addAPIError turns err into diagnostics. Field errors from the API
// (validation_failed, immutable_field) become attribute errors when the
// field maps onto an attribute of s; everything else is a general error that
// carries the API's message and code. s may be nil (provider configure).
//
// aliases renames API fields whose Terraform attribute has another name,
// e.g. {"provider": "provider_name"} ("provider" is reserved in Terraform).
//
//nolint:unparam // aliases: for resources whose attribute names differ from the API (provider credentials)
func addAPIError(ctx context.Context, diags *diag.Diagnostics, s schemaPaths, summary string, err error, aliases ...map[string]string) {
	apiErr, ok := client.AsAPIError(err)
	if !ok {
		diags.AddError(summary, err.Error())
		return
	}

	var unmapped []string
	for _, field := range apiErr.FieldNames() {
		msgs := strings.Join(apiErr.Fields[field], " ")
		if p, ok := attributePath(ctx, s, field, aliases...); ok {
			diags.AddAttributeError(p, summary, msgs+"\n\n"+errorFooter(apiErr))
			continue
		}
		unmapped = append(unmapped, fmt.Sprintf("  %s: %s", field, msgs))
	}
	if len(apiErr.Fields) > 0 && len(unmapped) == 0 {
		return // every message is attached to its attribute
	}

	var b strings.Builder
	b.WriteString(apiErr.Message)
	if apiErr.RequiredPlan != "" {
		fmt.Fprintf(&b, "\n\nThe %q feature needs the %q plan or above.", apiErr.Feature, apiErr.RequiredPlan)
	}
	if len(unmapped) > 0 {
		b.WriteString("\n\n" + strings.Join(unmapped, "\n"))
	}
	if hint := codeHints[apiErr.Code]; hint != "" {
		b.WriteString("\n\n" + hint)
	}
	b.WriteString("\n\n" + errorFooter(apiErr))
	diags.AddError(summary, b.String())
}

func errorFooter(e *client.APIError) string {
	s := fmt.Sprintf("routeur.ai API: HTTP %d", e.Status)
	if e.Code != "" {
		s += ", code " + e.Code
	}
	if e.RequestID != "" {
		s += ", request id " + e.RequestID
	}
	return s
}

// attributePath maps an API field ("name", "action.quality_bias",
// "match.any_of.0.model") onto an attribute path, if s has it.
func attributePath(ctx context.Context, s schemaPaths, field string, aliases ...map[string]string) (path.Path, bool) {
	if s == nil || field == "" {
		return path.Empty(), false
	}
	for _, a := range aliases {
		if to, ok := a[field]; ok {
			field = to
		}
	}
	segments := strings.Split(field, ".")
	p := path.Root(segments[0])
	for _, seg := range segments[1:] {
		if i, err := strconv.Atoi(seg); err == nil && i >= 0 {
			p = p.AtListIndex(i)
		} else {
			p = p.AtName(seg)
		}
	}
	if _, d := s.TypeAtPath(ctx, p); d.HasError() {
		return path.Empty(), false
	}
	return p, true
}
