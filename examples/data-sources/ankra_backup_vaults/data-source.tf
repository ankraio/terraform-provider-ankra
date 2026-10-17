data "ankra_backup_vaults" "all" {}

output "ready_vaults" {
  value = [for vault in data.ankra_backup_vaults.all.vaults : vault.name if vault.status == "ready"]
}
