resource "routeur_workspace" "payments" {
  slug = "payments"
  name = "Payments"
}

# $2,000 a month for the whole organisation; alert at 80%.
resource "routeur_budget" "organisation" {
  name         = "Organisation cap"
  scope_type   = "organisation"
  amount_cents = 200000
}

# $500 a week for one workspace, refusing requests once it is exceeded.
resource "routeur_budget" "payments" {
  name         = "Payments weekly"
  scope_type   = "workspace"
  scope_id     = routeur_workspace.payments.id
  period       = "weekly"
  amount_cents = 50000

  alert_threshold_pct = 90
  hard_block          = true
}
