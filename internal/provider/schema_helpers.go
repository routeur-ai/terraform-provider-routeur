package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// publicIDPattern matches an API public id such as ws_3 or proj_12.
func publicIDPattern(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `_[1-9][0-9]*$`)
}

// publicID validates a public id with the given prefix ("ws", "proj", …).
func publicID(prefix string) validator.String {
	return stringvalidator.RegexMatches(publicIDPattern(prefix), fmt.Sprintf("must be a routeur.ai id such as %s_12", prefix))
}

// hyphenSlugPattern is the workspace and project slug format.
var hyphenSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// underscoreSlugPattern is the policy and rule slug format.
var underscoreSlugPattern = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

// Slug length limits. A workspace slug is shorter than a project slug
// because the workspace's default project is created as `<slug>-core`.
const (
	workspaceSlugMax = 55
	projectSlugMax   = 60
)

// hyphenSlug validates a workspace or project slug of at most maxLen characters.
func hyphenSlug(maxLen int) []validator.String {
	return []validator.String{
		stringvalidator.UTF8LengthAtMost(maxLen), // "" is refused by the pattern
		stringvalidator.RegexMatches(hyphenSlugPattern, "must be lowercase letters, digits and hyphens, starting and ending with a letter or digit"),
	}
}

// underscoreSlug validates a policy or rule slug (DLP policies,
// moderation rules, …): lowercase letters and digits in runs joined by
// single underscores, at most 64 characters.
func underscoreSlug() []validator.String {
	return []validator.String{
		stringvalidator.UTF8LengthAtMost(64), // "" is refused by the pattern
		stringvalidator.RegexMatches(underscoreSlugPattern, "must be lowercase letters and digits joined by single underscores"),
	}
}

// name validates an identifier-like string: 1–max characters, not blank,
// no leading or trailing whitespace (the API refuses those with a 422).
// Lengths count characters (code points), as the API does, not bytes. The
// minimum is left to noSurroundingWhitespace, so "" is one error, not two.
func name(maxLen int) []validator.String {
	return []validator.String{stringvalidator.UTF8LengthAtMost(maxLen), noSurroundingWhitespace()}
}

// noSurroundingWhitespace refuses values that start or end with
// whitespace; the API stores strings verbatim and refuses those with a 422.
func noSurroundingWhitespace() validator.String { return surroundingWhitespaceValidator{} }

type surroundingWhitespaceValidator struct{}

func (surroundingWhitespaceValidator) Description(context.Context) string {
	return "must not be blank or start or end with whitespace"
}

func (v surroundingWhitespaceValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v surroundingWhitespaceValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if strings.TrimSpace(s) != s || strings.TrimSpace(s) == "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid value", fmt.Sprintf("%s %s, got %q.", req.Path, v.Description(ctx), s))
	}
}

// --- common attributes --------------------------------------------------

// idAttribute is the computed public id, stable for the resource's life.
func idAttribute(description string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// workspaceIDAttribute is the parent workspace of a workspace-nested
// resource: a ws_N id (use routeur_workspace.x.id); changing it replaces.
func workspaceIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Id of the workspace this belongs to (`ws_…`), e.g. `routeur_workspace.example.id`. " +
			"The `routeur_workspace` data source resolves a slug to its id. Changing it replaces the resource.",
		Required:      true,
		Validators:    []validator.String{publicID("ws")},
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

// createOnly marks an x-create-only string: set at create, changing it
// replaces the resource.
func createOnly() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.RequiresReplace()}
}

// createdAtAttribute and updatedAtAttribute are the server timestamps.
// updated_at deliberately has no UseStateForUnknown: it changes on update.
func createdAtAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "When it was created (RFC 3339, UTC).",
		CustomType:          timetypes.RFC3339Type{},
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func updatedAtAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "When it was last changed (RFC 3339, UTC).",
		CustomType:          timetypes.RFC3339Type{},
		Computed:            true,
	}
}

// --- value helpers -------------------------------------------------------

// nullableString is the JSON value for an optional, nullable string: nil
// (sent as null, which clears the field on PATCH) when null.
func nullableString(v types.String) any {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return v.ValueString()
}

// decimalFloat64 parses a JSON number the way Terraform parses one in
// configuration (a 512-bit big.Float), so an imported `0.755` equals the
// configured `0.755`. types.Float64Value(f) would not.
func decimalFloat64(n json.Number) types.Float64 {
	f, _, err := big.ParseFloat(n.String(), 10, 512, big.ToNearestEven)
	if err != nil {
		v, _ := strconv.ParseFloat(n.String(), 64) // not a JSON number: never from the API
		return types.Float64Value(v)
	}
	v, err := types.Float64Type.ValueFromTerraform(context.Background(), tftypes.NewValue(tftypes.Number, f))
	if fv, ok := v.(types.Float64); err == nil && ok {
		return fv
	}
	fv, _ := f.Float64() // beyond float64's range: the schema's validators keep values well inside it
	return types.Float64Value(fv)
}

// decimalFloat64Pointer is decimalFloat64 for a nullable number.
func decimalFloat64Pointer(n *json.Number) types.Float64 {
	if n == nil {
		return types.Float64Null()
	}
	return decimalFloat64(*n)
}

// re2Pattern refuses a pattern that is not valid RE2 syntax (lookarounds,
// backreferences, …), the syntax the gateway matches patterns with.
func re2Pattern() validator.String { return re2PatternValidator{} }

type re2PatternValidator struct{}

func (re2PatternValidator) Description(context.Context) string {
	return "must be a valid RE2 regular expression"
}

func (v re2PatternValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v re2PatternValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := regexp.Compile(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid regular expression", fmt.Sprintf("%s must be a valid RE2 regular expression: %s.", req.Path, err))
	}
}

// rfc3339 is a timestamp from the API, typed so Terraform knows it is an
// RFC 3339 time (and compares it as a time, not as text). The API always
// sends RFC 3339; a malformed value is kept as text rather than failing.
func rfc3339(s string) timetypes.RFC3339 {
	v, _ := timetypes.NewRFC3339Value(s)
	return v
}

// rfc3339Pointer is rfc3339 for a nullable timestamp.
func rfc3339Pointer(s *string) timetypes.RFC3339 {
	if s == nil {
		return timetypes.NewRFC3339Null()
	}
	return rfc3339(*s)
}

// growingEnum checks a value against the values this provider version
// knows, as a warning rather than an error, since the API may accept values
// added after this release. what names the kind of value, e.g. "provider".
func growingEnum(what string, known ...string) validator.String {
	return growingEnumValidator{what: what, known: known}
}

type growingEnumValidator struct {
	what  string
	known []string
}

func (v growingEnumValidator) Description(context.Context) string {
	return fmt.Sprintf("one of %s; other values are passed to the API with a warning", strings.Join(v.known, ", "))
}

func (v growingEnumValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v growingEnumValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	for _, k := range v.known {
		if s == k {
			return
		}
	}
	resp.Diagnostics.AddAttributeWarning(req.Path, "Unknown "+v.what,
		fmt.Sprintf("%q is not a %s this version of the routeur provider knows (%s); the routeur.ai API decides whether it accepts it. "+
			"If it is new in routeur.ai, upgrading the provider removes this warning; if it is a typo, the API will refuse it.",
			s, v.what, strings.Join(v.known, ", ")))
}
