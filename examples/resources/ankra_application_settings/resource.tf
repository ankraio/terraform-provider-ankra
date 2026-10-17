resource "ankra_application_settings" "this" {
  ci_runner_label                = "self-hosted-linux"
  auto_merge_setup_pull_requests = true
}
