# Copyright (c) HashiCorp, Inc.
# SPDX-License-Identifier: MPL-2.0

terraform {
  required_providers {
    ankra = {
      source  = "ankraio/ankra"
      version = "0.1.6"
    }
  }
}

provider "ankra" {
  token = var.ankra_token
}

resource "ankra_cluster" "example" {
  cluster_name           = "dev"
  github_credential_name = "my-github-cred"
  github_branch          = "main"
  github_repository      = "ankra-io/my-repo"
}

output "ankra_cluster_id" {
  value = ankra_cluster.example.cluster_id
}
