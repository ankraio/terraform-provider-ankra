data "ankra_cost_budgets" "all" {}

output "budgets_over" {
  value = [for budget in data.ankra_cost_budgets.all.budgets : budget.name if budget.status == "over"]
}
