resource "ankra_organisation_variable" "region" {
  name        = "default_region"
  value       = "fsn1"
  description = "Region new clusters default to"
}

resource "ankra_organisation_variable" "registry_password" {
  name      = "registry_password"
  value     = var.registry_password
  is_secret = true
}

variable "registry_password" {
  type      = string
  sensitive = true
}
