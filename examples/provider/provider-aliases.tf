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
