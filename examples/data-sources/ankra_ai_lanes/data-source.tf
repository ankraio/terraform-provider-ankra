data "ankra_ai_lanes" "all" {}

output "lane_models" {
  value = { for lane in data.ankra_ai_lanes.all.lanes : lane.lane => lane.effective_model_id }
}
