terraform {
  required_providers {
    ankra = {
      source  = "ankra-io/ankra"
      version = ">= 0.1.0"
    }
  }
}

provider "ankra" {}

resource "ankra_cluster" "example" {
  environment             = var.environment
  github_credential_name  = var.github_credential_name
  github_branch           = var.github_branch
  github_repository       = var.github_repository
  ankra_token             = var.ankra_token
  ci_user                 = var.ci_user
  bastion_ip              = var.bastion_ip
  bastion_user            = var.bastion_user
  controller_host         = var.controller_host
}

output "ankra_cluster_id" {
  description = "The cluster_id returned by the Ankra import operation"
  value       = ankra_cluster.example.cluster_id
}
