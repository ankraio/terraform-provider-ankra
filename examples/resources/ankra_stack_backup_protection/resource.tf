resource "ankra_stack_backup_protection" "shop" {
  cluster_id = "0b6f1d2e-1111-4222-8333-944445555666"
  stack_name = "shop"
  vault      = ankra_backup_vault.production.id
  schedule   = "0 1 * * *"

  retention = {
    daily   = 7
    weekly  = 4
    monthly = 6
  }

  selection = {
    persistent_volume_claims = ["shop/uploads"]
  }

  backup_now = true
}
