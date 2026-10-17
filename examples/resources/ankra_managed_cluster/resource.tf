resource "ankra_managed_cluster" "example" {
  provider_name = "aks"
  name          = "aks-prod"
  credential_id = "61262ddf-72db-4745-9a77-2c19961cf2d5"
  location      = "westeurope"

  # The provider's own options, sent under the "aks" key.
  provider_options = jsonencode({
    resource_group  = "rg-ankra"
    private_cluster = false
  })

  # node_count and autoscaling change in place; any other pool change
  # replaces the cluster. Further pools are ankra_managed_node_pool resources.
  node_pools = [{
    name       = "system"
    size       = "Standard_D4s_v5"
    node_count = 3
    autoscaling = {
      enabled   = true
      min_count = 3
      max_count = 6
    }
  }]
}
