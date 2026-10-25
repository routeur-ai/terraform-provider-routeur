package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// Spec coverage: the vendored OpenAPI spec (openapi/v1) against the
// provider. Every operation must be implemented by a resource or data
// source, or be listed below; every property of a response schema must be an
// attribute (or deliberately ignored). A new field in the spec fails this
// test until the provider exposes it.

// specCoverage is what one resource or data source implements. Register it
// from the resource's _test.go:
//
//	func init() {
//		registerSpecCoverage(specCoverage{
//			typeName:   "routeur_project",
//			schema:     "Project",
//			operations: []string{"createProject", "getProject", "updateProject", "deleteProject"},
//		})
//	}
type specCoverage struct {
	// typeName is the resource type, or "data.<type>" for a data source.
	typeName string
	// schema is the response schema (components/schemas/<schema>.json).
	schema string
	// operations are the operationIds the implementation calls.
	operations []string
	// aliases maps an API property (dotted for nested) to its attribute
	// name when they differ, e.g. {"provider": "provider_name"}.
	aliases map[string]string
	// ignore lists API properties deliberately not exposed, each with a
	// reason in a comment at the registration.
	ignore []string
}

var specCoverages []specCoverage

func registerSpecCoverage(c specCoverage) { specCoverages = append(specCoverages, c) }

// pendingOperations are not implemented yet. Delete a line when a resource
// or data source covers it; the test fails if a covered one is left here.
var pendingOperations = map[string]bool{}

// unusedOperations are deliberately not called by any resource or data
// source, each with the reason. They count as covered.
var unusedOperations = map[string]string{
	// Resources address items by id, so list endpoints need no data source.
	"listWorkspaces":          "resources address items by id; not exposed as a data source",
	"listAnomalyDetectors":    "resources address items by id; not exposed as a data source",
	"listBudgets":             "resources address items by id; not exposed as a data source",
	"listClassifierRules":     "resources address items by id; not exposed as a data source",
	"listDlpPolicies":         "resources address items by id; not exposed as a data source",
	"listModerationRules":     "resources address items by id; not exposed as a data source",
	"listPromptShieldRules":   "resources address items by id; not exposed as a data source",
	"listProviderCredentials": "resources address items by id; not exposed as a data source",
	"listRoutingRules":        "resources address items by id; not exposed as a data source",
	// Rotating revokes the old key at once; replacing the resource (with
	// create_before_destroy) gives callers time to switch keys.
	"rotateApiKey": "rotation is replacement: DELETE + POST",
	// Destroy revokes and removes the key with one DELETE; `enabled = false`
	// is the reversible alternative.
	"revokeApiKey": "destroy revokes and deletes in one DELETE",
	// A listed key's secret cannot be read, so it is of little use.
	"listApiKeys": "no routeur_api_keys data source",
}

const specDir = "../../openapi/v1"

// specOperations returns every operationId in the vendored spec.
func specOperations(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(specDir, "paths", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no spec in %s: %v", specDir, err)
	}
	ops := map[string]bool{}
	for _, f := range files {
		var doc map[string]map[string]json.RawMessage
		readJSON(t, f, &doc)
		for _, item := range doc {
			for _, raw := range item {
				var op struct {
					OperationID string `json:"operationId"`
				}
				if json.Unmarshal(raw, &op) == nil && op.OperationID != "" {
					ops[op.OperationID] = true
				}
			}
		}
	}
	return ops
}

func readJSON(t *testing.T, file string, v any) {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
}

func TestSpecOperationsCovered(t *testing.T) {
	ops := specOperations(t)
	covered := map[string]string{}
	for op := range unusedOperations {
		if !ops[op] {
			t.Errorf("unusedOperations lists %s, which is not in the spec", op)
		}
		covered[op] = "unusedOperations"
	}
	for _, c := range specCoverages {
		for _, op := range c.operations {
			if !ops[op] {
				t.Errorf("%s claims operation %s, which is not in the spec", c.typeName, op)
			}
			if _, unused := unusedOperations[op]; unused {
				t.Errorf("%s calls %s: delete it from unusedOperations", c.typeName, op)
			}
			covered[op] = c.typeName
		}
	}
	var missing []string
	for op := range ops {
		_, isCovered := covered[op]
		pending, listed := pendingOperations[op]
		switch {
		case isCovered && listed && pending:
			t.Errorf("%s is implemented by %s: delete it from pendingOperations", op, covered[op])
		case !isCovered && (!listed || !pending):
			missing = append(missing, op)
		}
	}
	sort.Strings(missing)
	for _, op := range missing {
		t.Errorf("spec operation %s is neither implemented nor listed in pendingOperations", op)
	}
	for op := range pendingOperations {
		if !ops[op] {
			t.Errorf("pendingOperations lists %s, which is not in the spec", op)
		}
	}
}

// jsonSchema is the subset of a JSON Schema the coverage check reads.
type jsonSchema struct {
	Properties map[string]*jsonSchema `json:"properties"`
	ReadOnly   bool                   `json:"readOnly"`
}

func TestSpecSchemasCovered(t *testing.T) {
	ctx := context.Background()
	server := providerserver.NewProtocol6(New("test")())()
	resp, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range specCoverages {
		if c.schema == "" {
			continue
		}
		var s *tfprotov6.Schema
		if name, ok := strings.CutPrefix(c.typeName, "data."); ok {
			s = resp.DataSourceSchemas[name]
		} else {
			s = resp.ResourceSchemas[c.typeName]
		}
		if s == nil {
			t.Errorf("%s is registered for spec coverage but not served by the provider", c.typeName)
			continue
		}
		var js jsonSchema
		readJSON(t, filepath.Join(specDir, "components", "schemas", c.schema+".json"), &js)
		ignore := map[string]bool{}
		for _, i := range c.ignore {
			ignore[i] = true
		}
		checkProperties(t, c, "", js.Properties, s.Block.Attributes, ignore)
	}
}

func checkProperties(t *testing.T, c specCoverage, prefix string, props map[string]*jsonSchema, attrs []*tfprotov6.SchemaAttribute, ignore map[string]bool) {
	t.Helper()
	byName := map[string]*tfprotov6.SchemaAttribute{}
	for _, a := range attrs {
		byName[a.Name] = a
	}
	for prop, sub := range props {
		key := prefix + prop
		if ignore[key] {
			continue
		}
		name := prop
		if alias, ok := c.aliases[key]; ok {
			name = alias
		}
		a, ok := byName[name]
		if !ok {
			t.Errorf("%s: spec property %s (%s.json) has no attribute %q", c.typeName, key, c.schema, name)
			continue
		}
		if a.Description == "" {
			t.Errorf("%s: attribute %q has no description", c.typeName, name)
		}
		if sub != nil && len(sub.Properties) > 0 && a.NestedType != nil {
			checkProperties(t, c, key+".", sub.Properties, a.NestedType.Attributes, ignore)
		}
	}
}

// Every resource and data source schema must be valid for Terraform (no
// reserved names such as "provider" or "count", no missing descriptions).
func TestProviderSchemasValid(t *testing.T) {
	server := providerserver.NewProtocol6(New("test")())()
	resp, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Diagnostics {
		t.Errorf("%s: %s", d.Summary, d.Detail)
	}
	check := func(kind, name string, s *tfprotov6.Schema) {
		if s.Block.Description == "" {
			t.Errorf("%s %s has no description", kind, name)
		}
		var walk func(prefix string, attrs []*tfprotov6.SchemaAttribute)
		walk = func(prefix string, attrs []*tfprotov6.SchemaAttribute) {
			for _, a := range attrs {
				if a.Description == "" {
					t.Errorf("%s %s: attribute %s%s has no description", kind, name, prefix, a.Name)
				}
				if strings.Contains(a.Description, "Routeur ") || strings.Contains(a.Description, "routeur ") {
					t.Errorf("%s %s: attribute %s%s: write the product as routeur.ai", kind, name, prefix, a.Name)
				}
				if a.NestedType != nil {
					walk(prefix+a.Name+".", a.NestedType.Attributes)
				}
			}
		}
		walk("", s.Block.Attributes)
	}
	for name, s := range resp.ResourceSchemas {
		check("resource", name, s)
	}
	for name, s := range resp.DataSourceSchemas {
		check("data source", name, s)
	}
	check("provider", "routeur", resp.Provider)
}
