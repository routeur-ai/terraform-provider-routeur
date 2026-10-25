resource "routeur_provider_credential" "openai" {
  provider_name = "openai"
  label         = "prod"
  key           = var.openai_api_key
}

data "routeur_workspace" "default" {
  slug = "default"
}

# Route the default workspace to two OpenAI models. Destroying a resource
# deactivates its model again.
resource "routeur_workspace_model" "openai" {
  for_each = toset(["gpt-4o", "gpt-4o-mini"])

  workspace_id = data.routeur_workspace.default.id
  # Referencing the credential creates it before the models need it.
  model = "${routeur_provider_credential.openai.provider_name}:${each.key}"
}

variable "openai_api_key" {
  type      = string
  sensitive = true
}
