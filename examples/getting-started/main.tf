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
