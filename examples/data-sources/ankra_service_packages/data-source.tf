data "ankra_service_packages" "all" {}

output "first_party_packages" {
  value = [for package in data.ankra_service_packages.all.packages : "${package.name}@${package.version}" if package.first_party]
}
