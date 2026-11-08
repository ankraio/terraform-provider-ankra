data "ankra_cloud_dns_zones" "all" {}

locals {
  apps_zone = one([for zone in data.ankra_cloud_dns_zones.all.zones : zone if zone.name == "apps.acme.ankra.app"])
}

# Only admins and operators who can reach the platform project may open
# hostnames in the zone.
resource "ankra_cloud_dns_zone_access" "apps" {
  zone_id    = local.apps_zone.id
  mode       = "organisation"
  roles      = ["admin", "operator"]
  project_id = "platform"
}
