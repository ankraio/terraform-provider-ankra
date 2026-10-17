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

  # Pools are keyed by name. node_count and autoscaling change in place;
  # adding or removing a key adds or deletes a pool. To change a pool's size,
  # give it a new key: the new pool is added before the old one is deleted.
  node_pools = {
    system = {
      size       = "Standard_D4s_v5"
      node_count = 3
      autoscaling = {
        enabled   = true
        min_count = 3
        max_count = 6
      }
    }
  }

  # A cluster this resource creates is deleted at the provider on destroy;
  # an imported one is only disconnected unless this says otherwise.
  destroy_mode = "delete_provider_cluster"

  # Also wait for the Ankra agent, not just for the provider's build.
  wait_for_agent = true
}
