# Contributing

Thanks for helping improve the routeur.ai Terraform provider. Bug reports,
documentation fixes and pull requests are welcome.

- **Bugs and feature requests:** open a GitHub issue using one of the
  templates. Include the provider and Terraform versions, the configuration
  (with secrets removed) and the error, including the routeur.ai request id
  the provider prints with every API error.
- **Security issues:** do not open an issue; see [SECURITY.md](SECURITY.md).
- **Conduct:** everyone taking part follows the
  [Code of Conduct](CODE_OF_CONDUCT.md).
- **Pull requests:** keep them focused, include unit tests, run `make fmt lint
  test docs` before pushing, and commit the regenerated `docs/`.

The provider builds and its unit tests run without a routeur.ai account.
Acceptance tests need a non-production routeur.ai environment; routeur.ai
maintainers run them before every release (see
[README.md](README.md#acceptance-tests)).

The rest of this file lists the conventions the provider follows.

## Design and conventions

The provider is a client of the routeur.ai management API
(`https://app.routeur.ai/api/v1`), which manages configuration only; AI
traffic goes to `api.routeur.ai`. The contract is
[`openapi/v1`](openapi/v1), the published management API spec (OpenAPI 3.1)
for the routeur.ai platform version in `openapi/VERSION`. Where this file and
the spec disagree, the spec wins.

### Stack

- Go (version in `go.mod`), the
  [Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework)
  (not SDKv2), protocol 6.
- `terraform-plugin-framework-timetypes` for RFC 3339 timestamps,
  `terraform-plugin-framework-validators` for validation,
  `terraform-plugin-log/tflog` for logging.
- `terraform-plugin-testing` for acceptance tests and `terraform-plugin-docs`
  (tfplugindocs) for `docs/`.
- No generated client: a small hand-written `internal/client` package. New
  dependencies need a one-line reason in the pull request.
- Module path `github.com/routeur-ai/terraform-provider-routeur`; registry
  address `registry.terraform.io/routeur-ai/routeur`, one constant in
  `main.go`.

### Provider configuration

```hcl
provider "routeur" {
  token    = "..."  # sensitive; env ROUTEUR_TOKEN
  org      = "acme" # optional; env ROUTEUR_ORG; default: the token's organisation
  base_url = "..."  # optional; env ROUTEUR_BASE_URL; default https://app.routeur.ai/api/v1
}
```

- The environment variable is not `ROUTEUR_KEY`, which is the gateway key
  applications send to `api.routeur.ai`.
- An unknown or missing token at configure time is a diagnostic, with no
  network call.
- Configure always calls `GET /me`, so a bad token is one provider error and
  a configured `org` that is not the token's organisation fails before any
  plan.
- Several organisations: provider aliases. There is no per-resource `org`.
- User-Agent: `terraform-provider-routeur/<version> (+terraform <tfversion>)`.

### Client (`internal/client`)

- One `Client` with `Do(ctx, method, path, in, out)`; JSON in and out;
  `Authorization: Bearer`.
- The error envelope `{message, code, errors?}` becomes
  `*APIError{Status, Code, Message, Fields}`; `IsNotFound(err)` tests for 404.
- Retries: 429 always (honouring `Retry-After`, else exponential backoff with
  jitter) for up to 5 minutes from the first attempt; 502/503/504 and network
  errors only for idempotent methods (GET, PUT, DELETE), at most 5 attempts.
  POST and PATCH are never re-sent once the request may have reached the
  server. Every wait is bounded by the context deadline.
- `Deprecation` (RFC 9745) and `Sunset` (RFC 8594) response headers are
  collected per operation (`client.WithNotices`); `registerResource` and
  `registerDataSource` wrap every resource and data source so each distinct
  notice becomes one warning diagnostic.
- `ListAll[T]` follows the cursor in `meta.next_cursor` with the maximum page
  size.
- Path segments are escaped with `url.PathEscape` (model ids contain `/` and
  `:`).
- Never log the token or credential request bodies; tflog gets request lines
  only (method, path, status, request id).

### Resource conventions

Every resource follows these.

- Names are `routeur_<snake_singular>`. Ids are the API's public ids (`ws_3`,
  `proj_9`, `route_4`, `key_2`, `cred_7`, `dlp_1`, `mod_1`, `shield_1`,
  `clf_1`, `anom_1`, `bud_1`).
- Workspace-nested resources take `workspace_id` (the `ws_N` id; changing it
  replaces the resource). Users pass `routeur_workspace.x.id`; there is one
  accepted form, and the `routeur_workspace` data source resolves a slug to an
  id.
- Attribute classes come from the spec:
  - read-only (`id`, `created_at`, fingerprints, …): Computed, plus
    `UseStateForUnknown()` where the value is stable (not `updated_at`);
  - `x-create-only`: `RequiresReplace()`;
  - optional with a server default: Optional + Computed, so omitting it is
    not a diff.
- Create = POST; Read = GET (404 removes the resource from state); Update =
  PUT or PATCH as the spec gives (PUT sends the full desired object, PATCH
  only changed fields); Delete = DELETE (404 counts as success).
- Import: organisation-level resources by id (`bud_3`); workspace-nested
  resources by `<workspace_id>/<id>`; singletons by `<workspace_id>` (default
  route) or the organisation slug (moderation settings). Every resource
  implements ImportState and has an ImportStateVerify acceptance step, and an
  `import.sh` and `import-by-string-id.tf` example.
- Validation mirrors the spec (enums, lengths, ranges, patterns) with
  framework validators, so `terraform validate` and `plan` fail early. Enums
  the spec says may grow are validated on input only, and an unknown value
  read back never fails. `provider_name` is validated as a warning, because
  new upstream providers can arrive without a provider release; the API
  decides.
- Lengths count characters (UTF-8 code points), as the API does, never bytes.
  Timestamps are `timetypes.RFC3339`. Order-insensitive string lists are sets.
- Nested objects are typed (SingleNestedAttribute, ListNestedAttribute),
  never JSON strings. Polymorphic shapes (routing `action`, DLP detector,
  anomaly `threshold`) are one nested object with optional fields plus
  ExactlyOneOf / ConflictsWith validators, so plans show field-level diffs.
- Strings are stored verbatim by the API; the provider never trims or
  normalises them.
- Optional values with a fixed server default (`enabled` = true,
  `quality_bias` = 50, …) are Optional + Computed with a schema `Default`, so
  removing the argument returns the resource to the default and the value is
  always sent. Only values the server derives (not a constant) use Computed +
  `UseStateForUnknown`.
- `provider` is reserved by Terraform, so the attribute is `provider_name`,
  aliased to the API's `provider` in `addAPIError` and in the spec coverage
  test.
- No drift loops: after an apply, a plan must be empty. Every acceptance test
  ends with a plan-only step asserting no changes
  (`plancheck.ExpectEmptyPlan()`).
- Diagnostics: API field errors map onto attribute paths
  (`AddAttributeError`) when the field is a known attribute; anything else is
  a general error with the API's `code`, `message` and request id. Error codes
  whose message alone does not say what to do in Terraform get a hint in
  `codeHints` (`internal/provider/diag.go`).

### Special resources

- `routeur_workspace`: `slug` is create-only; `default_project_id` is
  computed. Deleting a workspace also removes its default project and Default
  route on the server.
- `routeur_api_key` (a gateway key for `api.routeur.ai`): `secret` is
  Computed + Sensitive and returned only on create, so it is kept in state and
  Read never overwrites it. A change to `rotation_triggers` **replaces** the
  key (with `create_before_destroy`, callers can switch before the old key is
  deleted). When the stored triggers are null (just imported), new triggers
  are stored in place, so adopting a live key never revokes it. `enabled` and `project_id`
  change in place (PATCH). A key revoked outside Terraform is replaced on the
  next plan.
- `routeur_provider_credential`: one per provider per organisation;
  `provider_name` is create-only. The key is set one of two ways:
  - `key`: Sensitive, kept in state; a change rotates the key in place.
  - `key_wo` + `key_wo_version`: write-only (Terraform 1.11 or later), never
    in plan or state; bump `key_wo_version` to rotate.

  Drift is detected through the computed `fingerprint`: if Read returns a
  different fingerprint, the key is marked as changed so the next apply sends
  it again.
- `routeur_default_route`: a singleton per workspace that the server creates
  and never deletes. Create is a PUT; Delete PUTs the API default back
  (quality bias 50) and removes it from state.
- `routeur_moderation_settings`: an organisation singleton; Delete resets the
  API default.
- `routeur_workspace_model`: `PUT {active}`; id
  `<workspace_id>/<provider>:<model_id>`; Delete sets `active = false`
  (models are opt-in). Model metadata is computed.
- Budgets, DLP policies, moderation rules, prompt-shield rules, classifier
  rules, anomaly detectors and routing rules are ordinary CRUD.

### Data sources

`routeur_me`, `routeur_workspace` (by `slug` or `id`), `routeur_project` (by
workspace and `slug`, or `id`) and `routeur_workspace_models` (a list, with
optional `active` and `provider` filters applied client-side).

### Layout

```
main.go
internal/client/        client.go, errors.go, paginate.go, notices.go (+ unit tests with httptest)
internal/provider/      provider.go, registry.go, one file per resource / data source,
                        diag.go (API errors to diagnostics), import.go (id parsing),
                        acc_test.go helpers (+ <resource>_test.go acceptance tests),
                        spec_test.go (spec coverage)
examples/               provider/, resources/<name>/{resource.tf,import.sh,import-by-string-id.tf},
                        data-sources/<name>/data-source.tf, getting-started/, import/
templates/              tfplugindocs templates (index page, subcategories)
docs/                   generated by tfplugindocs; never edit by hand
openapi/                published management API spec
```

Resources register themselves through `registry.go`: each resource file has
`func init() { registerResource(NewXResource) }` (data sources:
`registerDataSource`), so adding a resource touches no shared file.

### Documentation

`docs/` is generated: change a schema's `MarkdownDescription`, an example
under `examples/` or a template under `templates/`, then run `make docs`. CI
fails when `docs/` is out of date. A resource's subcategory on the Registry
comes from its template in `templates/resources/` or `templates/data-sources/`;
a new resource needs one.

### Testing

- Unit tests (`make test`) need no network: the client (retries,
  `Retry-After`, error envelope, pagination, path escaping) and pure helpers
  (id parsing, diagnostics) run against `httptest` servers.
- Acceptance tests (`TF_ACC=1`, `make testacc`) run against a non-production
  routeur.ai environment, never against `app.routeur.ai` (the tests refuse
  to). Each
  resource covers create → import verify → update in place → replace (where a
  create-only argument changes) → empty plan → destroy, plus at least one
  validation failure (`ExpectError`).

### Spec drift guard

- `internal/provider/spec_test.go` loads the vendored spec and fails when any
  request or response property of a covered schema has no mapped attribute
  and is not on an explicit allowlist with a reason, or when a resource sends
  a field the spec doesn't have.
- A provider release that needs a new API field ships only after that
  platform version is live. v1 of the API is additive, so older provider
  versions keep working.

### Changelog

Add an entry to `CHANGELOG.md` under the top `(Unreleased)` heading, in the
section it belongs to: `NOTES`, `FEATURES`, `ENHANCEMENTS` or `BUG FIXES`
(and `BREAKING CHANGES` after 1.0). Prefix each entry with its scope:
`* resource/routeur_x: ...`, `* data-source/routeur_x: ...` or
`* provider: ...`. New objects are listed as `* **New Resource:** \`routeur_x\``.
See the [changelog specification](https://developer.hashicorp.com/terraform/plugin/best-practices/versioning#changelog-specification).

### Versioning

- Semver tags `vX.Y.Z`, independent of the platform version.
- After 1.0, schema changes follow the API's rule: additive only within a
  major version.
