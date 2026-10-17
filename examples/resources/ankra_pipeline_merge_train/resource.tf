resource "ankra_pipeline_merge_train" "platform" {
  repository_id = ankra_pipeline_repository.platform.id
  enabled       = true
  max_cars      = 3
}
