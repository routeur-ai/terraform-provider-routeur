data "routeur_workspace" "default" {
  slug = "default"
}

# Look a project up by slug…
data "routeur_project" "core" {
  workspace_id = data.routeur_workspace.default.id
  slug         = "core"
}

# …or by id, e.g. the workspace's default project.
data "routeur_project" "default" {
  workspace_id = data.routeur_workspace.default.id
  id           = data.routeur_workspace.default.default_project_id
}
