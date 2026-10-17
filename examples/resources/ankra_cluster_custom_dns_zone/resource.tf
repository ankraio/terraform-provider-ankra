data "ankra_cluster" "prod" {
  name = "prod"
}

resource "ankra_cluster_custom_dns_zone" "launch" {
  cluster_id          = data.ankra_cluster.prod.id
  zone                = "launch.example.com"
  credential_name     = "example-cloudflare"
  credential_provider = "cloudflare"
}
