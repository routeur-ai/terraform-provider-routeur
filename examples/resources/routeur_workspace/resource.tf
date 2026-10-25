resource "routeur_workspace" "payments" {
  slug        = "payments"
  name        = "Payments"
  description = "Checkout, refunds and fraud screening."
}

# Every workspace comes with a default project; API keys created without a
# project land there.
output "payments_default_project" {
  value = routeur_workspace.payments.default_project_id
}
