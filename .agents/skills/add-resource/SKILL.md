---
name: add-resource
description: Add a new resource or data source to the routeur.ai Terraform provider end to end (spec check, schema, CRUD, import, tests, examples, docs template, changelog). Use when asked to add, implement or expose a routeur_* resource or data source, or to map a new API endpoint into the provider.
---

# Add a resource or data source

Conventions live in CONTRIBUTING.md ("Resource conventions", "Testing"); the
spec in `openapi/v1` wins over any prose. Use `internal/provider/budget_resource.go`
and `budget_resource_test.go` as the model for an ordinary CRUD resource, and
`workspace_models_data_source.go` or `project_data_source.go` for data sources.

## 1. Check the spec

- [ ] Find the operations in `openapi/v1/paths/*.json` (note each
      `operationId`) and the response schema in
      `openapi/v1/components/schemas/<Schema>.json`.
- [ ] Do not invent fields. If the endpoint or a field is not in the spec,
      stop and say so; the spec is updated through the `update-api-spec` skill.
- [ ] Classify every property: read-only (`readOnly`), create-only
      (`x-create-only`), optional with a server default, required.
- [ ] Note enums, lengths, ranges and patterns, and whether the spec says an
      enum may grow.

## 2. Write `internal/provider/<name>_resource.go`

- [ ] Type name `routeur_<snake_singular>`; `func init() { registerResource(NewXResource) }`
      (data sources: `registerDataSource`). Do not edit `registry.go`.
- [ ] Embed `resourceBase` with `name` set to the part after `routeur_`
      (`dataSourceBase` for data sources). Build paths with
      `r.orgPath(...)` or `r.workspacePath(workspaceID, ...)`.
- [ ] An `apiX` struct for the API schema and a `xModel` struct with `tfsdk`
      tags; a `fromAPI` method copies one to the other.
- [ ] Attribute classes, using `schema_helpers.go`:
      - id: `idAttribute(...)`; workspace parent: `workspaceIDAttribute()`;
      - read-only: Computed + `UseStateForUnknown()` where stable (not
        `updated_at`); timestamps via `createdAtAttribute()` /
        `updatedAtAttribute()` and `timetypes.RFC3339`;
      - `x-create-only`: `createOnly()` (RequiresReplace);
      - optional with a fixed server default: Optional + Computed + a schema
        `Default`; only server-derived values use Computed alone.
- [ ] Validators mirror the spec: `name(max)`, `hyphenSlug`, `underscoreSlug`,
      `publicID(prefix)`, `stringvalidator.OneOf`, `int64validator.Between`,
      `re2Pattern()`. Lengths count characters, not bytes. An enum the spec
      says may grow uses `growingEnum` (input only, a warning). Cross-field
      rules go in `ValidateConfig` (see the budget `scope_id` check) or
      `ExactlyOneOf` / `ConflictsWith`. Order-insensitive lists are sets;
      nested objects are typed, never JSON strings.
- [ ] Every attribute has a `MarkdownDescription` (a test fails otherwise).
      Write the product as `routeur.ai`.
- [ ] Create = POST via `client.Send[apiX]`; Read = `client.Get[apiX]` and on
      `client.IsNotFound(err)` call `resp.State.RemoveResource(ctx)`; Update =
      PUT (full object) or PATCH (only changed fields) as the spec gives;
      Delete = DELETE, treating `client.IsNotFound` as success.
- [ ] Every API error goes through `addAPIError(ctx, &resp.Diagnostics, schema,
      "Could not <verb> <thing>", err)`. If an error code needs a Terraform
      next step, add it to `codeHints` in `diag.go`. If an API field name
      differs from the attribute (for example `provider`), pass an alias map.
- [ ] Import: `ImportState` validates the id (`publicIDPattern("bud")` then
      `resource.ImportStatePassthroughID`); workspace-nested resources call
      `importWorkspaceChild(ctx, req, resp, "<prefix>")` for
      `<workspace_id>/<id>`.
- [ ] Secrets: `Sensitive`, never logged; follow `api_key_resource.go` or
      `provider_credential_resource.go` if a value is returned only once.
- [ ] A list endpoint: paginate with `client.ListAll[T]`.

## 3. Tests: `internal/provider/<name>_resource_test.go`

- [ ] `TestAcc<Name>_basic`: create (check ids and server defaults with
      `statecheck`) -> `ImportState` + `ImportStateVerify` (use
      `testAccImportID` for nested resources) -> update in place
      (`plancheck.ExpectResourceAction(..., ResourceActionUpdate)`) -> replace
      for a create-only change (`plancheck.ResourceActionReplace`) -> a final step with
      `plancheck.ExpectEmptyPlan()`. Set `CheckDestroy` with
      `testAccCheckGone`. Use `testAccSlug` for unique names and
      `testAccPreCheck` in `PreCheck`.
- [ ] `TestAcc<Name>_validation`: `PlanOnly: true` steps with `ExpectError`
      for each validator and cross-field rule.
- [ ] Register spec coverage in the test file:
      `registerSpecCoverage(specCoverage{typeName, schema, operations, aliases, ignore})`.
      An `ignore` entry needs a comment giving the reason.
- [ ] `spec_test.go`: remove the operations from `pendingOperations` and
      `unusedOperations` if listed. Add to `unusedOperations` only with a
      reason. Run `go test ./internal/provider -run Spec`.
- [ ] Add unit tests for any pure helper. Acceptance tests need a non-production
      environment; do not run them without one (see AGENTS.md).

## 4. Examples and docs

- [ ] `examples/resources/routeur_<name>/resource.tf` (placeholders only),
      `import.sh` (`terraform import ...`) and `import-by-string-id.tf`
      (an `import { to = ...; id = "..." }` block). Data source:
      `examples/data-sources/routeur_<name>/data-source.tf`.
- [ ] `templates/resources/<name>.md.tmpl` (or `templates/data-sources/`):
      copy a neighbour such as `budget.md.tmpl` and set `subcategory` to an
      existing one. A resource without a template has no Registry subcategory.
- [ ] `make fmt lint test docs`; commit the regenerated `docs/`.
- [ ] `CHANGELOG.md`, under the Unreleased section, `FEATURES:`:
      `* **New Resource:** \`routeur_<name>\`` (or `**New Data Source:**`).
      Alphabetical within its group.
- [ ] Update CONTRIBUTING.md only if you introduced a new convention.
