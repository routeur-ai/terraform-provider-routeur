resource "routeur_workspace" "support" {
  slug = "support"
  name = "Support"
}

# Requests no routing rule matched lean towards cheaper models.
resource "routeur_default_route" "support" {
  workspace_id = routeur_workspace.support.id
  quality_bias = 30
}
