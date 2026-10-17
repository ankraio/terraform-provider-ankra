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
# project_id (or ANKRA_PROJECT_ID) takes the project's ID or its slug; a slug is
# resolved to the ID once, before the first request.
provider "ankra" {
  token      = var.ankra_token
  project_id = "platform"
}

variable "ankra_token" {
  type      = string
  sensitive = true
}
