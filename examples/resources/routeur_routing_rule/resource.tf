resource "routeur_workspace" "support" {
  slug = "support"
  name = "Support"
}

resource "routeur_project" "helpdesk" {
  workspace_id = routeur_workspace.support.id
  slug         = "helpdesk"
  name         = "Helpdesk"
}

# A rule can only name models activated in the workspace, and a provider's
# models need a credential first.
resource "routeur_provider_credential" "openai" {
  provider_name = "openai"
  label         = "prod"
  key           = var.openai_api_key
}

resource "routeur_workspace_model" "support" {
  for_each = toset(["gpt-4o", "gpt-4o-mini"])

  workspace_id = routeur_workspace.support.id
  model        = "${routeur_provider_credential.openai.provider_name}:${each.key}"
}

variable "openai_api_key" {
  type      = string
  sensitive = true
}

# Pin image requests from the helpdesk project to one model.
resource "routeur_routing_rule" "images" {
  workspace_id = routeur_workspace.support.id
  project_id   = routeur_project.helpdesk.id
  name         = "Images to GPT-4o"
  priority     = 10

  match = {
    contains_image = true
  }

  action = {
    pin = {
      provider = routeur_workspace_model.support["gpt-4o"].provider_name
      model    = routeur_workspace_model.support["gpt-4o"].model_id
    }
  }
}

# Long prompts or prompts about invoices: two condition groups, either may match.
# Fail over between two models, retrying each once.
resource "routeur_routing_rule" "long_or_billing" {
  workspace_id = routeur_workspace.support.id
  name         = "Long or billing prompts"
  priority     = 20

  match = {
    any_of = [
      { min_input_tokens = 20000 },
      { prompt_pattern = "(?i)\\b(invoice|refund)\\b" },
    ]
  }

  action = {
    failover = {
      strategy = "sequential"
      targets = [
        for m in ["gpt-4o", "gpt-4o-mini"] : {
          provider = routeur_workspace_model.support[m].provider_name
          model    = routeur_workspace_model.support[m].model_id
        }
      ]
    }
  }

  on_failure = {
    retries      = 1
    wait_seconds = 1
    then         = "next"
  }
}

# Callers asking for "fast" get the cheapest model that clears a low quality bar.
resource "routeur_routing_rule" "fast" {
  workspace_id = routeur_workspace.support.id
  name         = "Fast alias"
  priority     = 30

  match = {
    requested_model = "fast"
  }

  action = {
    auto = { quality_bias = 10 }
  }
}
