data "routeur_workspace" "default" {
  slug = "default"
}

# Every OpenAI model the default workspace can route to.
data "routeur_workspace_models" "openai" {
  workspace_id  = data.routeur_workspace.default.id
  provider_name = "openai"
}

# The models the workspace routes to today.
data "routeur_workspace_models" "active" {
  workspace_id = data.routeur_workspace.default.id
  active       = true
}

output "active_models" {
  value = data.routeur_workspace_models.active.models[*].model
}

output "openai_catalogue" {
  value = {
    for m in data.routeur_workspace_models.openai.models :
    m.model => { name = m.display_name, input_per_1k = m.input_per_1k }
  }
}
