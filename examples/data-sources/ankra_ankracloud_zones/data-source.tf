# Without credential_id the organisation's built-in Ankra Cloud credential is used.
data "ankra_ankracloud_zones" "all" {}

output "ankracloud_zones" {
  value = data.ankra_ankracloud_zones.all.zones[*].name
}
