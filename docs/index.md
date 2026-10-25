---
page_title: "routeur.ai Provider"
description: |-
  Manage routeur.ai configuration as code through the management API.
---

# routeur.ai Provider

[routeur.ai](https://routeur.ai) is an LLM gateway: your applications send AI
traffic to `api.routeur.ai`, which routes each request to an upstream model
and applies your policies on the way. This provider manages the gateway's
configuration — workspaces, projects, API keys, provider credentials, models,
routing rules, safety policies, budgets and anomaly detectors — through the
management API at `https://app.routeur.ai/api/v1`. It never sends AI traffic
itself.

## Requirements

- Terraform 1.0 or later.
- Terraform 1.11 or later to use the write-only `key_wo` argument of
  [`routeur_provider_credential`](resources/provider_credential), which keeps a
  provider API key out of the plan and state.
- A routeur.ai organisation and an API token (see
  [Authentication](#authentication)).

## Example Usage

```terraform
terraform {
  required_providers {
    routeur = {
      source  = "routeur-ai/routeur"
      version = "~> 0.1"
    }
  }
}

# Authenticates with a routeur.ai API token (rtm_…), created in
# app.routeur.ai under Settings → API tokens:
#   export ROUTEUR_TOKEN="rtm_..."
provider "routeur" {
  # Optional safety check: the provider refuses to run unless the token
  # belongs to this organisation.
  # org = "acme"
}
```

Run `terraform init` to install the provider from the Terraform Registry.

## Authentication

The provider authenticates with an **API token** (`rtm_…`), not an API key.
API keys (`ROUTEUR_KEY`) are what your applications use to call models through
`api.routeur.ai`; API tokens are for managing routeur.ai itself:

1. Sign in to [app.routeur.ai](https://app.routeur.ai) and open
   **Settings → API tokens**.
2. Create a token and copy it; it is shown only once.
3. Export it before running Terraform:

   ```shell
   export ROUTEUR_TOKEN="rtm_..."
   ```

A token acts as the user who created it, in their organisation, with exactly
their permissions, so a Terraform run can do what that user can do in the app
and no more. If your organisation requires multi-factor authentication, you
must be enrolled to create a token, and the token stops working (error
`mfa_required`) while its owner is not enrolled.

Prefer the environment variable to the `token` argument, so the token never
appears in configuration or version control. To create API keys for your
applications with Terraform, use [`routeur_api_key`](resources/api_key).

### Pinning the organisation

A token only works in its own organisation. Setting `org` (or `ROUTEUR_ORG`)
is an optional safety check: the provider compares it with the token's
organisation when it starts and stops with an error if they differ, instead of
planning against the wrong organisation.

```terraform
provider "routeur" {
  org = "acme"
}
```

### Several organisations

To manage several organisations from one configuration, declare one provider
block per organisation with an `alias` and its own token, and choose it per
resource with `provider`:

```terraform
# One provider block per organisation, each with its own API token (rtm_…).
variable "acme_token" {
  type      = string
  sensitive = true
}

variable "acme_eu_token" {
  type      = string
  sensitive = true
}

provider "routeur" {
  alias = "acme"
  org   = "acme"
  token = var.acme_token
}

provider "routeur" {
  alias = "acme_eu"
  org   = "acme-eu"
  token = var.acme_eu_token
}

resource "routeur_workspace" "eu_payments" {
  provider = routeur.acme_eu

  slug = "payments"
  name = "Payments (EU)"
}
```

## Getting started

This configuration creates a workspace and a project, a gateway API key for
the project, an OpenAI credential (write-only, so the key never reaches
state), two OpenAI models activated in the workspace, and a routing rule that
sends long prompts to the larger model. Every dependency is an attribute
reference, so Terraform creates and destroys things in the right order with
no `depends_on`.

```terraform
terraform {
  # key_wo below is a write-only argument, which needs Terraform 1.11 or later.
  required_version = ">= 1.11"

  required_providers {
    routeur = {
      source  = "routeur-ai/routeur"
      version = "~> 0.1"
    }
  }
}

# Reads the API token (rtm_…) from ROUTEUR_TOKEN.
provider "routeur" {}

# The OpenAI key, e.g. from TF_VAR_openai_api_key. Ephemeral: it is never
# written to the plan or state.
variable "openai_api_key" {
  type      = string
  sensitive = true
  ephemeral = true
}

# A workspace holds routing rules and policies; it comes with a default
# project and a Default route.
resource "routeur_workspace" "support" {
  slug = "support"
  name = "Support"
}

resource "routeur_project" "helpdesk" {
  workspace_id = routeur_workspace.support.id
  slug         = "helpdesk"
  name         = "Helpdesk"
}

# The gateway key the helpdesk service sends to api.routeur.ai.
resource "routeur_api_key" "helpdesk" {
  workspace_id = routeur_workspace.support.id
  project_id   = routeur_project.helpdesk.id
  name         = "Helpdesk service"
}

# routeur.ai calls OpenAI with your own key. key_wo keeps it out of state;
# bump key_wo_version to send a new key.
resource "routeur_provider_credential" "openai" {
  provider_name  = "openai"
  label          = "prod"
  key_wo         = var.openai_api_key
  key_wo_version = 1
}

# Models are opt-in per workspace. Building `model` from the credential's
# provider_name makes Terraform create the credential first.
resource "routeur_workspace_model" "openai" {
  for_each = toset(["gpt-4o", "gpt-4o-mini"])

  workspace_id = routeur_workspace.support.id
  model        = "${routeur_provider_credential.openai.provider_name}:${each.key}"
}

# Long prompts go to the larger model. Everything else falls through to the
# workspace's Default route, which picks a model automatically.
resource "routeur_routing_rule" "long_prompts" {
  workspace_id = routeur_workspace.support.id
  name         = "Long prompts"
  priority     = 10

  match = {
    min_input_tokens = 8000
  }

  action = {
    pin = {
      provider = routeur_workspace_model.openai["gpt-4o"].provider_name
      model    = routeur_workspace_model.openai["gpt-4o"].model_id
    }
  }
}

# The secret is only returned when the key is created.
output "helpdesk_routeur_key" {
  value     = routeur_api_key.helpdesk.secret
  sensitive = true
}
```

```shell
export ROUTEUR_TOKEN="rtm_..."
export TF_VAR_openai_api_key="sk-..."
terraform init
terraform apply
terraform output -raw helpdesk_routeur_key   # the key for api.routeur.ai
```

## Importing existing configuration

Everything this provider manages can be imported, so configuration first
created in the routeur.ai app can be brought under Terraform. Each resource
page lists its import id: organisation-level resources use their id (for
example `bud_3`), resources inside a workspace use `<workspace_id>/<id>` (for
example `ws_3/proj_12`), and a workspace also accepts its slug. With Terraform
1.5 or later, use an `import` block:

```terraform
# Adopt a workspace created in the routeur.ai app. Import ids are listed on
# each resource's page; a workspace also accepts its slug.
import {
  to = routeur_workspace.support
  id = "support"
}

resource "routeur_workspace" "support" {
  slug = "support"
  name = "Support"
}
```

`terraform plan -generate-config-out=generated.tf` can write a starting
configuration for imported resources; review it before applying. The
[`routeur_workspace`](data-sources/workspace) and
[`routeur_project`](data-sources/project) data sources look up existing objects
by slug when you only need their ids.

## Propagation

A successful apply means routeur.ai has stored the change. Configuration then
reaches the gateway asynchronously — usually within seconds, at worst a few
minutes — so a request sent immediately after `terraform apply` may still see
the previous configuration. Budgets and anomaly detectors act on their next
evaluation pass rather than at the moment of the apply.

## Rate limits and retries

The management API allows each token 600 reads and 120 writes a minute (and
1,200 requests a minute from one IP address). The provider handles the limits
for you:

- A rate-limited request (`429`) is retried after the delay the API asks for,
  for up to 5 minutes.
- Idempotent requests (`GET`, `PUT`, `DELETE`) are retried on `502`, `503`,
  `504` and network errors, up to 5 attempts. A `POST` or `PATCH` is retried
  only when it never reached the server, so a retry cannot create a duplicate.

For very large configurations, a lower `terraform apply -parallelism=N` keeps
runs smooth. Give concurrent pipelines their own tokens, since the limits are
per token.

## Deprecations

When the API marks an endpoint or field as deprecated, the provider shows a
warning naming it and, where announced, the date it stops working.

<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `base_url` (String) Base URL of the management API. May also be set with the `ROUTEUR_BASE_URL` environment variable. Defaults to `https://app.routeur.ai/api/v1`.
- `org` (String) Slug of the organisation to manage. May also be set with the `ROUTEUR_ORG` environment variable. Defaults to the token's own organisation. An API token only works in its own organisation, so the provider checks this against `GET /me` when it starts and fails if they differ (rather than finding nothing and planning to recreate everything). To manage several organisations, use one provider alias, with its own token, per organisation.
- `token` (String, Sensitive) A routeur.ai API token (`rtm_…`), created in [app.routeur.ai](https://app.routeur.ai) under **Settings → API tokens**. It acts as the user who minted it, in their organisation, with their permissions. Prefer the `ROUTEUR_TOKEN` environment variable, which keeps it out of configuration; it is used when this argument is unset. This is not an API key: API keys (`ROUTEUR_KEY`) are what your applications use to call models through `api.routeur.ai`.
