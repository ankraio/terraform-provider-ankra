data "ankra_stack_profiles" "all" {}

output "own_profiles" {
  value = [for profile in data.ankra_stack_profiles.all.profiles : profile.name if profile.is_owner]
}
