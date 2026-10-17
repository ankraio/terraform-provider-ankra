resource "ankra_registry_robot" "cluster_pull" {
  name        = "cluster-pull"
  project     = ankra_registry_project.ci.name
  scope       = "pull"
  description = "Image pulls for the production cluster"

  # Change the value to rotate the secret in place.
  rotation_triggers = {
    rotated = "2026-10"
  }
}

output "docker_login" {
  value     = ankra_registry_robot.cluster_pull.docker_login
  sensitive = true
}
