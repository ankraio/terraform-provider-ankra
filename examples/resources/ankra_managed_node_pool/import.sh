# Managed node pools are imported by <provider_name>/<cluster_id>/<name>. A pool this
# resource resized imports by the name it was created with; the first refresh finds the
# pool that replaced it.
terraform import ankra_managed_node_pool.batch aks/ef8cf93e-1d97-4494-9de4-12ced709ffde/batch
