resource "ankra_cost_autopilot_project_default" "payments" {
  project_id = "7e6f5a4b-3c2d-4e1f-a0b9-c8d7e6f5a4b3"
  defaults = {
    production = "managed"
    preview    = "ephemeral"
  }
}
