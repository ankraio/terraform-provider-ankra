resource "ankra_cost_autopilot" "policy" {
  defaults = {
    staging     = "scheduled"
    development = "managed"
    preview     = "ephemeral"
  }

  quiet_hours = {
    start    = "22:00"
    end      = "06:00"
    timezone = "Europe/Stockholm"
  }
}
