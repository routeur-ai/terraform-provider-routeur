resource "routeur_workspace" "payments" {
  slug = "payments"
  name = "Payments"
}

resource "routeur_project" "checkout" {
  workspace_id = routeur_workspace.payments.id
  slug         = "checkout"
  name         = "Checkout"
}

# Redact card numbers and email addresses (managed presets) everywhere in the workspace.
resource "routeur_dlp_policy" "pii" {
  workspace_id = routeur_workspace.payments.id
  slug         = "pii"
  name         = "PII"
  detector     = "preset"
  presets      = ["email", "credit_card"]
  action       = "redact"
  severity     = "high"
}

# Block a custom RE2 pattern, for one project only.
resource "routeur_dlp_policy" "internal_ticket" {
  workspace_id = routeur_workspace.payments.id
  project_id   = routeur_project.checkout.id
  slug         = "internal_ticket"
  name         = "Internal ticket ids"
  detector     = "regex"
  pattern      = "\\bPAY-[0-9]{3,6}\\b"
  action       = "block"
  severity     = "medium"
}

# Warn on keywords (case-insensitive substrings).
resource "routeur_dlp_policy" "codenames" {
  workspace_id = routeur_workspace.payments.id
  slug         = "codenames"
  name         = "Codenames"
  detector     = "keyword"
  keywords     = ["project falcon", "project osprey"]
  action       = "warn"
  severity     = "low"
}

# Block known secret formats.
resource "routeur_dlp_policy" "secrets" {
  workspace_id = routeur_workspace.payments.id
  slug         = "secrets"
  name         = "Secrets"
  detector     = "secret"
  secrets      = ["adobe-client-id", "1password-secret-key"]
  action       = "block"
  severity     = "high"
  enabled      = false
}
