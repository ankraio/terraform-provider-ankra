data "ankra_playground_plans" "all" {}

output "available_playground_plans" {
  value = [for plan in data.ankra_playground_plans.all.plans : plan.id if plan.available]
}
