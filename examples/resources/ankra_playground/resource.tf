data "ankra_playground_plans" "all" {}

resource "ankra_playground" "example" {
  # "trial" is the free plan; a paid plan resizes in place to another paid plan.
  plan = "starter"
}

output "playground_phase" {
  value = ankra_playground.example.phase
}
