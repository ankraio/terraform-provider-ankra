resource "ankra_helm_registry_credential" "acme" {
  name     = "acme-charts"
  username = "robot$acme"
  password = var.acme_registry_token
}

resource "ankra_helm_registry" "acme" {
  name              = "acme"
  url               = "oci://registry.acme.example/charts"
  credential_name   = ankra_helm_registry_credential.acme.name
  read_job_interval = 600
}

resource "ankra_helm_registry" "bitnami" {
  name           = "bitnami"
  url            = "https://charts.bitnami.com/bitnami"
  exclude_charts = ["wordpress"]
}

# An ankra_cluster addon then names the registry it installs from:
#
#   addons {
#     name                     = "api"
#     chart_name               = "api"
#     chart_version            = "1.4.0"
#     registry_name            = ankra_helm_registry.acme.name
#     registry_url             = ankra_helm_registry.acme.url
#     registry_credential_name = ankra_helm_registry_credential.acme.name
#     namespace                = "api"
#   }

variable "acme_registry_token" {
  type      = string
  sensitive = true
}
