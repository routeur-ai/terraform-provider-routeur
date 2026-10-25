resource "routeur_workspace" "platform" {
  slug = "platform"
  name = "Platform"
}

# Alert when spend in a day passes $500 (the threshold is in US cents).
resource "routeur_anomaly_detector" "daily_spend" {
  workspace_id   = routeur_workspace.platform.id
  name           = "Daily spend"
  type           = "cost_spike"
  threshold      = { amount_cents = 50000 }
  window_minutes = 1440
  action         = "alert"
}

# Refuse traffic while more than 5% of calls fail over 15 minutes.
resource "routeur_anomaly_detector" "errors" {
  workspace_id   = routeur_workspace.platform.id
  name           = "Error burst"
  type           = "error_burst"
  threshold      = { error_rate_pct = 5 }
  window_minutes = 15
  action         = "block"
}

resource "routeur_anomaly_detector" "latency" {
  workspace_id   = routeur_workspace.platform.id
  name           = "Slow responses"
  type           = "latency_spike"
  threshold      = { p95_ms = 8000 }
  window_minutes = 60
  action         = "alert"
}

resource "routeur_anomaly_detector" "volume" {
  workspace_id   = routeur_workspace.platform.id
  name           = "Traffic surge"
  type           = "volume_spike"
  threshold      = { baseline_pct = 300 }
  window_minutes = 30
  action         = "alert"
}
