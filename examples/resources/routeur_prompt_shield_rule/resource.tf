resource "routeur_workspace" "payments" {
  slug = "payments"
  name = "Payments"
}

resource "routeur_prompt_shield_rule" "override" {
  workspace_id = routeur_workspace.payments.id
  name         = "Instruction override"
  slug         = "instruction_override"
  category     = "instruction_override"
  detector     = "keyword"
  keywords     = ["ignore previous instructions", "disregard the above"]
  action       = "block"
  severity     = "high"
}

resource "routeur_prompt_shield_rule" "system_leak" {
  workspace_id = routeur_workspace.payments.id
  name         = "System prompt leak"
  slug         = "system_prompt_leak"
  category     = "system_leak"
  detector     = "regex"
  pattern      = "(?i)reveal (your )?system prompt"
  action       = "warn"
  severity     = "medium"
}
