data "ankra_ankracloud_plans" "all" {}

locals {
  # Plans are listed cheapest first.
  smallest_four_core_plan = [for plan in data.ankra_ankracloud_plans.all.plans : plan.name if plan.cores >= 4][0]
}
