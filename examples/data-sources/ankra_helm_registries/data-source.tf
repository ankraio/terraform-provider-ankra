data "ankra_helm_registries" "all" {}

output "registry_names" {
  value = [for registry in data.ankra_helm_registries.all.registries : registry.name]
}
