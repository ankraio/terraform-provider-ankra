data "ankra_registry_robots" "all" {}

output "expiring_robots" {
  value = [for robot in data.ankra_registry_robots.all.robots : robot.name if robot.expires_at != null]
}
