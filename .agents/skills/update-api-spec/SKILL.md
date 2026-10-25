---
name: update-api-spec
description: Vendor a new published version of the routeur.ai management API OpenAPI spec into openapi/v1 and openapi/VERSION, then map or allowlist the new and removed fields in the Terraform provider. Use when asked to update, bump, sync or refresh the API spec, or when spec_test.go fails after the spec changes.
---

# Update the vendored API spec

The provider depends only on the public spec in `openapi/v1` and the platform
version in `openapi/VERSION`. Only vendor a spec the contributor supplies as
a published version; do not hand-write spec content or guess at fields.

## Steps

- [ ] Get the new published spec and its platform version number from the
      contributor (the public docs are at https://docs.routeur.ai). If you
      have neither, stop and ask.
- [ ] Replace the contents of `openapi/v1` with the new spec files
      (`openapi.json`, `paths/*.json`, `components/**`) and write the new
      version to `openapi/VERSION`. Keep the file layout and formatting of
      the published files; do not reformat them. Remove files the new spec
      dropped.
- [ ] Review `git diff -- openapi` to list added, removed and changed
      operations, schemas, properties, enums and `x-create-only` flags.
- [ ] Run `go test ./internal/provider -run Spec` (`TestSpecOperationsCovered`,
      `TestSpecSchemasCovered`, `TestProviderSchemasValid`). Failures name
      each unmapped operation or property.
- [ ] For each new response property: add the attribute to the resource and
      its `apiX` / model structs (see the `add-resource` skill for attribute
      classes, validators and descriptions), and send it on create or update
      when it is a request field. If it is deliberately not exposed, add it to
      `ignore` in that resource's `registerSpecCoverage` call with a comment
      giving the reason; use `aliases` only for a renamed attribute.
- [ ] For each new operation: implement it, or add it to `unusedOperations`
      or `pendingOperations` in `spec_test.go` with a reason. Remove entries
      for operations that no longer exist (the test fails on them).
- [ ] For each removed or renamed property or operation: check whether the
      spec marks it deprecated. API v1 is additive, so a removal is unexpected;
      stop and ask before removing a provider attribute.
- [ ] Tighten or relax validators only to match changed lengths, ranges, enums
      and patterns in the spec.
- [ ] Update affected acceptance tests and examples. Do not run acceptance
      tests without a non-production environment.
- [ ] `make fmt lint test docs`; commit the regenerated `docs/`.

## Compatibility rules

- API v1 is additive, so older provider versions keep working.
- After 1.0, provider schema changes are additive only within a major
  version: new optional or computed attributes, never removals, renames or
  newly required arguments.
- A provider release that needs a new API field ships only after that
  platform version is live. State the required platform version in the
  changelog.

## Changelog

Under the Unreleased section of `CHANGELOG.md`:

- [ ] `ENHANCEMENTS:` lines such as `resource/routeur_budget: new \`x\` argument ...`.
- [ ] Update the `NOTES:` line that says which routeur.ai platform version
      the provider requires (the value in `openapi/VERSION`) when the new
      fields need it.
