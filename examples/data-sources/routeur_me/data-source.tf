data "routeur_me" "current" {}

output "organisation" {
  value = data.routeur_me.current.organisation.slug
}

output "token_expires_at" {
  value = data.routeur_me.current.token.expires_at
}
