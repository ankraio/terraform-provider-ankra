terraform {
  required_providers {
    ankra = {
      source  = "ankraio/ankra"
      version = "0.1.2"
    }
  }
}

provider "ankra" {}

resource "ankra_cluster" "example" {
  environment             = "dev"
  github_credential_name  = "my-github-cred"
  github_branch           = "main"
  github_repository       = "ankra-io/my-repo"
  ankra_token             = var.ankra_token
}

output "ankra_cluster_id" {
  value = ankra_cluster.example.cluster_id
}
