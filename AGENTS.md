# AGENTS.md

Instructions for AI coding agents working in this repository. Humans: see
[CONTRIBUTING.md](CONTRIBUTING.md), which is the source of truth for
conventions. This file does not repeat it; read the relevant section before
changing code.

## Project

Terraform provider for the routeur.ai management API
(`https://app.routeur.ai/api/v1`), written in Go with the Terraform Plugin
Framework (not SDKv2), protocol 6. Licence: MPL-2.0. The provider manages
configuration only; AI traffic goes to `api.routeur.ai`.

## Commands

```sh
make fmt lint test docs   # run all four before finishing any change
make testacc              # acceptance tests (TF_ACC=1); see below
```

- `make docs` regenerates `docs/` (tfplugindocs). Commit the result.
- `make lint` runs golangci-lint when installed, else `go vet`.
- Acceptance tests create and delete real objects. They need
  `ROUTEUR_TOKEN`, `ROUTEUR_ORG` and `ROUTEUR_BASE_URL` pointing at
  a routeur.ai non-production environment, and refuse to run against
  `app.routeur.ai`. Do not run `make testacc` or set `TF_ACC` unless the
  contributor has given you such an environment. Without one, write the
  acceptance tests, make sure they compile (`go vet ./...`), and say they were
  not run.

## Repo map

```
main.go                  provider entry point and registry address
internal/client/         hand-written HTTP client: Do, Get, Send, ListAll, Path,
                         APIError, IsNotFound, WithNotices
internal/provider/       provider.go, registry.go (registerResource /
                         registerDataSource), one file per resource or data
                         source, diag.go (addAPIError, codeHints), import.go,
                         schema_helpers.go, <resource>_test.go, acc_test.go,
                         spec_test.go (spec coverage)
examples/                resources/<name>/, data-sources/<name>/, provider/, ...
templates/               tfplugindocs templates (subcategory per resource)
docs/                    GENERATED. Never edit by hand.
openapi/v1, openapi/VERSION   vendored public API spec and its platform version
```

## Hard rules

- **The spec is the contract.** The provider depends only on the vendored
  `openapi/v1` and `openapi/VERSION`. Where the spec and prose (including
  CONTRIBUTING.md) disagree, the spec wins. Never invent API fields, endpoints,
  enum values or error codes. `internal/provider/spec_test.go` must pass.
- **`docs/` is generated.** Change a schema `MarkdownDescription`, an example
  or a template, then run `make docs`. CI fails when `docs/` is stale.
- **No drift.** After an apply, a plan must be empty. Every acceptance test
  ends with an empty-plan step.
- **Secrets.** Never log or print tokens, keys or credential request bodies.
  Mark secrets `Sensitive`. Examples and tests use obvious placeholders, never
  real credentials.
- **Retries.** POST and PATCH are never re-sent once the request may have
  reached the server. Open an issue before changing the retry policy in
  `internal/client`.
- **Comments** are short godoc and non-obvious "why" only. No history, change
  notes, or restating the code.
- **Brand.** In prose, descriptions and docs the product is always
  `routeur.ai` (lower case, with the dot), never "Routeur". `provider "routeur"`
  and `routeur_*` names are code, not prose.
- **UK spelling** in prose and comments (organisation, behaviour); golangci
  `misspell` runs with locale UK. API enum values keep the API's spelling.
- **Dependencies.** Add none without a one-line reason in the pull request.
- **Scope.** Keep changes focused; do not reformat or refactor unrelated code.
- **Changelog.** Add a user-facing entry under the Unreleased section of
  `CHANGELOG.md`, in the existing format (`NOTES:`, `FEATURES:`, `ENHANCEMENTS:`
  sections; lines such as `resource/routeur_budget: ...`).
- **Compatibility.** API v1 is additive. After 1.0, provider schema changes are
  additive only within a major version.

## Where to look

- Resource, import, validation and diagnostics conventions: CONTRIBUTING.md,
  "Resource conventions" and "Special resources".
- Testing shape and the spec drift guard: CONTRIBUTING.md, "Testing" and
  "Spec drift guard".
- Docs: CONTRIBUTING.md, "Documentation".
- A good ordinary CRUD example: `internal/provider/budget_resource.go` and
  `budget_resource_test.go`.

## Skills

Reusable task checklists live in `.agents/skills/` (Agent Skills format,
`SKILL.md` per directory). `.claude/skills/` links to the same directories.

- `add-resource`: add a resource or data source end to end.
- `update-api-spec`: vendor a new published version of the API spec.
- `review-change`: review a diff or pull request against these conventions.
