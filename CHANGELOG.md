## 0.1.0 (October 25, 2026)

NOTES:

* First release of the Terraform provider for the routeur.ai management API (`https://app.routeur.ai/api/v1`). Requires Terraform 1.0 or later; the write-only `key_wo` argument needs Terraform 1.11 or later.
* Requires routeur.ai platform 1.2.1 or later (recorded in `openapi/VERSION`).

FEATURES:

* **New Resource:** `routeur_anomaly_detector`
* **New Resource:** `routeur_api_key`
* **New Resource:** `routeur_budget`
* **New Resource:** `routeur_classifier_rule`
* **New Resource:** `routeur_default_route`
* **New Resource:** `routeur_dlp_policy`
* **New Resource:** `routeur_moderation_rule`
* **New Resource:** `routeur_moderation_settings`
* **New Resource:** `routeur_project`
* **New Resource:** `routeur_prompt_shield_rule`
* **New Resource:** `routeur_provider_credential`
* **New Resource:** `routeur_routing_rule`
* **New Resource:** `routeur_workspace`
* **New Resource:** `routeur_workspace_model`
* **New Data Source:** `routeur_me`
* **New Data Source:** `routeur_project`
* **New Data Source:** `routeur_workspace`
* **New Data Source:** `routeur_workspace_models`

ENHANCEMENTS:

* provider: authenticates with an API token from `ROUTEUR_TOKEN`; the optional `org` argument (or `ROUTEUR_ORG`) is checked against the token's organisation before any plan, and provider aliases manage several organisations.
* provider: retries rate-limited requests (`429`, honouring `Retry-After`) for up to 5 minutes, and idempotent requests on `502`/`503`/`504` and network errors; a create or partial update is never re-sent once it may have reached the server.
* provider: API errors are attached to the offending attribute where possible and carry the API's error code, request id and, for common codes, the next step to take.
* provider: API deprecation notices (`Deprecation` and `Sunset` headers) are shown as warnings.
* provider: every resource supports import, with `import` block examples on every resource page.
* resource/routeur_provider_credential: write-only `key_wo` with `key_wo_version` keeps the provider API key out of plan and state; key drift is detected through `fingerprint`.
* resource/routeur_api_key: `rotation_triggers` replaces the key (use `create_before_destroy` for a zero-downtime rotation); the secret is kept in state as a sensitive value.
* resource/routeur_routing_rule: typed `match` conditions with OR-ed `any_of` groups, `pin` / `auto` / `failover` actions and an `on_failure` retry policy.
