resource "routeur_workspace" "payments" {
  slug = "payments"
  name = "Payments"
}

resource "routeur_project" "checkout" {
  workspace_id = routeur_workspace.payments.id
  slug         = "checkout"
  name         = "Checkout"
  description  = "The checkout service."
}

resource "routeur_project" "fraud" {
  workspace_id = routeur_workspace.payments.id
  slug         = "fraud-screening"
  name         = "Fraud screening"
  enabled      = false
}
