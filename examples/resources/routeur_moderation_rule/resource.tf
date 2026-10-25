resource "routeur_workspace" "payments" {
  slug = "payments"
  name = "Payments"
}

# A managed category.
resource "routeur_moderation_rule" "profanity" {
  workspace_id = routeur_workspace.payments.id
  name         = "Profanity"
  slug         = "profanity"
  category     = "profanity"
  action       = "redact"
  severity     = "low"
}

# A custom pattern, scoped to one project and scanned as tokens stream.
resource "routeur_project" "checkout" {
  workspace_id = routeur_workspace.payments.id
  slug         = "checkout"
  name         = "Checkout"
}

resource "routeur_moderation_rule" "card_numbers" {
  workspace_id = routeur_workspace.payments.id
  project_id   = routeur_project.checkout.id
  name         = "Card numbers"
  slug         = "card_numbers"
  category     = "regex"
  pattern      = "\\b\\d{16}\\b"
  action       = "block"
  severity     = "high"
  streaming    = "chunked"
}
