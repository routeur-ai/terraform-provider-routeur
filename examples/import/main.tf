# Adopt a workspace created in the routeur.ai app. Import ids are listed on
# each resource's page; a workspace also accepts its slug.
import {
  to = routeur_workspace.support
  id = "support"
}

resource "routeur_workspace" "support" {
  slug = "support"
  name = "Support"
}
