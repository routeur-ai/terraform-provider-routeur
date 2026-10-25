resource "routeur_workspace" "support" {
  slug = "support"
  name = "Support"
}

# Reject prompts the classifier scores as weapons-related at 0.85 or above.
resource "routeur_classifier_rule" "no_weapons" {
  workspace_id = routeur_workspace.support.id
  slug         = "no_weapons"
  name         = "No weapons"
  axis         = "weapons"
  min_score    = 0.85
  action       = "block"
}

# Only report coding prompts.
resource "routeur_classifier_rule" "coding" {
  workspace_id = routeur_workspace.support.id
  slug         = "coding"
  name         = "Coding"
  axis         = "coding"
  min_score    = 0.5
  action       = "log"
  enabled      = false
}
