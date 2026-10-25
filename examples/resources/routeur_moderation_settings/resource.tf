# One per organisation. Destroying it resets the default to `buffered`.
resource "routeur_moderation_settings" "this" {
  streaming_moderation = "chunked"
}
