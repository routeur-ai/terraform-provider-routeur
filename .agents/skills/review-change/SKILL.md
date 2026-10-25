---
name: review-change
description: Review a diff, branch or pull request for the routeur.ai Terraform provider against this repository's conventions (drift, secrets, 404 handling, retries, validators vs spec, generated docs, comments, brand, changelog). Use when asked to review, check or sign off a change, PR or patch.
---

# Review a change

Read the diff (`git diff`, or the PR's changed files), then CONTRIBUTING.md and
AGENTS.md for anything the checklist leaves open. Report findings by file and
line, most serious first, separating must-fix from suggestions. Do not
rewrite the change unless asked.

## Correctness

- [ ] **Spec**: every field sent or read exists in `openapi/v1` (no invented
      fields). Validators (enums, lengths, ranges, patterns) match the spec;
      lengths use UTF-8 character counts. An enum the spec says may grow is
      validated on input only (`growingEnum`), and an unknown value read back
      never fails.
- [ ] **Attribute classes**: read-only is Computed (+ `UseStateForUnknown()`
      when stable, never on `updated_at`); `x-create-only` has
      `RequiresReplace()`; an optional value with a fixed server default is
      Optional + Computed + `Default`.
- [ ] **No drift**: after apply, the plan is empty. The provider never trims
      or normalises strings. Numbers use `decimalFloat64`; timestamps use
      `timetypes.RFC3339`; order-insensitive lists are sets.
- [ ] **CRUD**: Read removes the resource from state on `client.IsNotFound`;
      Delete treats 404 as success; Update uses PUT (full object) or PATCH
      (only changed fields) as the spec gives; nothing is sent twice.
- [ ] **Errors**: API errors go through `addAPIError` with a "Could not ..."
      summary and the request's schema; new codes needing guidance are in
      `codeHints`; renamed fields use the aliases argument.
- [ ] **Import**: `ImportState` validates the id format; nested resources use
      `<workspace_id>/<id>`; there is an `ImportStateVerify` test step.
- [ ] **Client**: retries unchanged in spirit: 429 always, 502/503/504 and
      network errors only for GET, PUT and DELETE; POST and PATCH are never
      re-sent. Path segments go through `client.Path` / `url.PathEscape`.
      List endpoints use `client.ListAll`. Registration is via
      `registerResource` / `registerDataSource` (so deprecation notices are
      surfaced), not a shared file edit.

## Security

- [ ] No token, key or credential body is logged, printed, put in an error
      message, or committed. Secret attributes are `Sensitive`; write-only
      values are not stored in state.
- [ ] Examples and tests use placeholders, not real credentials, hostnames or
      account data.
- [ ] Tests never target `app.routeur.ai` (`testAccPreCheck` refuses).

## Tests

- [ ] Unit tests cover new helpers and client behaviour (`httptest`).
- [ ] Each resource has create -> import verify -> update -> replace (where a
      create-only argument exists) -> empty plan, and at least one
      `ExpectError` validation test.
- [ ] `registerSpecCoverage` is present; `ignore` entries have a reason;
      `pendingOperations` / `unusedOperations` are accurate.
- [ ] Acceptance tests that were not run are stated as such.

## Docs, style and housekeeping

- [ ] `docs/` is regenerated and matches `make docs`, and was not hand-edited.
      Examples exist (`resource.tf`, `import.sh`, `import-by-string-id.tf`) and
      a template with a subcategory exists in `templates/`.
- [ ] Comments are short godoc and non-obvious "why" only: no history, no
      "added for ..." notes, no restating the code.
- [ ] Prose, descriptions and docs say `routeur.ai`, not "Routeur"; UK
      spelling (organisation, behaviour).
- [ ] `CHANGELOG.md` has an entry under Unreleased in the existing format.
- [ ] No new dependency without a stated reason; the change is focused, with
      no unrelated reformatting.
- [ ] Schema changes are additive after 1.0, and a change needing a new API
      field notes the platform version it requires.
- [ ] `make fmt lint test docs` passes (state which you ran).
