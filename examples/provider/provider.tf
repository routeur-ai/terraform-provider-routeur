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
