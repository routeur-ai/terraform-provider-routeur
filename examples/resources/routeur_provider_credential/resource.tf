# Key kept in state (marked sensitive). Changing it rotates the credential
# in place.
resource "routeur_provider_credential" "openai" {
  provider_name = "openai"
  label         = "prod"
  key           = var.openai_api_key
}

# Write-only key (Terraform 1.11 or later): never stored in the plan or
# state. Bump key_wo_version to send a new key.
resource "routeur_provider_credential" "anthropic" {
  provider_name  = "anthropic"
  label          = "prod"
  key_wo         = var.anthropic_api_key
  key_wo_version = 1
}

variable "openai_api_key" {
  type      = string
  sensitive = true
}

variable "anthropic_api_key" {
  type      = string
  sensitive = true
  ephemeral = true
}
