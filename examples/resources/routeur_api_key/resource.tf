resource "routeur_workspace" "payments" {
  slug = "payments"
  name = "Payments"
}

resource "routeur_project" "checkout" {
  workspace_id = routeur_workspace.payments.id
  slug         = "checkout"
  name         = "Checkout"
}

# A gateway key for the checkout service. Changing rotation_triggers
# replaces the key; create_before_destroy keeps the old one working until
# the new one exists.
resource "routeur_api_key" "checkout" {
  workspace_id = routeur_workspace.payments.id
  project_id   = routeur_project.checkout.id
  name         = "Checkout service"

  rotation_triggers = {
    rotated = "2026-10"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# The secret is only returned when the key is created; hand it to the
# service, e.g. through a secret manager.
output "checkout_routeur_key" {
  value     = routeur_api_key.checkout.secret
  sensitive = true
}

# A key kept but switched off: the gateway refuses it until re-enabled.
resource "routeur_api_key" "legacy" {
  workspace_id = routeur_workspace.payments.id
  name         = "Legacy batch"
  enabled      = false
}
