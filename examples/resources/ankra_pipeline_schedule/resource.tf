# An application is connected to Ankra Pipelines once its setup pull request
# is merged. Apply a schedule that addresses it by application_id after that,
# in a second run, or address a connected repository by repository_id.
resource "ankra_pipeline_schedule" "nightly" {
  application_id = "5b0c4a9e-2f43-4b8e-9a51-1f0d8e7c2a10"
  cron           = "0 3 * * *"
  timezone       = "Europe/Oslo"

  inputs = {
    suite = "full"
  }
}
