terraform {
  required_providers {
    ankra = {
      source = "ankraio/ankra"
    }
  }
}

# The token can also be supplied via the ANKRA_TOKEN environment variable,
# and base_url via ANKRA_BASE_URL.
provider "ankra" {
  token = var.ankra_token
}

variable "ankra_token" {
  type      = string
  sensitive = true
}
