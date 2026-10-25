# The organisation's default workspace, created at sign-up.
data "routeur_workspace" "default" {
  slug = "default"
}

resource "routeur_project" "batch" {
  workspace_id = data.routeur_workspace.default.id
  slug         = "batch-jobs"
  name         = "Batch jobs"
}
