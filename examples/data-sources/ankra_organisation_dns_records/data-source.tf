data "ankra_organisation_dns_records" "all" {}

output "record_names" {
  value = data.ankra_organisation_dns_records.all.records[*].name
}
