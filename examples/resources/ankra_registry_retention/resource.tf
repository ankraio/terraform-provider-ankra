resource "ankra_registry_retention" "this" {
  default = {
    keep_days   = 30
    keep_latest = 10
  }

  rules = [
    {
      repository_pattern = "ankra-ci/**"
      keep_days          = 0
      keep_latest        = 3
    },
  ]
}
