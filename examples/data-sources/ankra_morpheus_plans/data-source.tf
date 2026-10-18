data "ankra_morpheus_plans" "all" {
  credential_id = ankra_morpheus_credential.main.id
}

output "morpheus_plans" {
  value = { for plan in data.ankra_morpheus_plans.all.plans : plan.name => plan.id }
}
