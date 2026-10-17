# Critical alert firings page the on-call engineer, and no later rule sees them.
resource "ankra_alert_route" "critical_to_pager" {
  destination_id = ankra_alert_destination.pagerduty.id
  kinds          = ["alert_trigger_fired"]
  severity       = "critical"
  priority       = 10
  stop_on_match  = true
}

# Every kind except pipeline runs goes to the platform channel.
resource "ankra_alert_route" "all_but_pipelines" {
  destination_id = ankra_alert_destination.platform_channel.id
  kinds          = ["pipeline_run_failed", "pipeline_run_succeeded"]
  kinds_negated  = true
}

# A provider scoped to one project still owns organisation-wide routes, which
# match every project's notifications.
resource "ankra_alert_route" "critical_everywhere" {
  destination_id    = ankra_alert_destination.pagerduty.id
  kinds             = ["alert_trigger_fired"]
  severity          = "critical"
  organisation_wide = true
}
