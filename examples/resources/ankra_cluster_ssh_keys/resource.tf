data "ankra_cluster" "prod" {
  name = "prod"
}

resource "ankra_cluster_ssh_keys" "prod" {
  cluster_id = data.ankra_cluster.prod.id
  ssh_key_credential_ids = [
    "5d0c3f1a-6a8e-4c39-9f0b-2e7d1c4b8a90",
  ]
}
