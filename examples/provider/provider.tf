terraform {
  required_providers {
    ankra = {
      source = "ankraio/ankra"
    }
  }
}

# The token can also be supplied via the ANKRA_TOKEN environment variable,
# base_url via ANKRA_BASE_URL, and organisation_id (to act in an organisation
# other than the one the token is bound to) via ANKRA_ORG (the CLI's variable for --org).
provider "ankra" {
  token = var.ankra_token
}

variable "ankra_token" {
  type      = string
  sensitive = true
}
