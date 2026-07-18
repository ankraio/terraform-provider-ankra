# Copyright (c) HashiCorp, Inc.
# SPDX-License-Identifier: MPL-2.0

terraform {
  required_providers {
    ankra = {
      source  = "local/ankra"
      version = "0.1.0"
    }
  }
}

# Token is read from the provider block, or the ANKRA_TOKEN environment
# variable when omitted. base_url falls back to ANKRA_BASE_URL.
provider "ankra" {
  token = var.ankra_token
}

resource "ankra_cluster" "example" {
  cluster_name           = "dev"
  github_credential_name = "my-github-cred"
  github_branch          = "main"
  github_repository      = "ankra-io/my-repo"

  stacks {
    name        = "create-ns-test"
    description = "Test stack for creating a namespace"

    manifests {
      name            = "test-namespace"
      manifest_base64 = base64encode(<<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: test-ns
YAML
      )
    }
    # Optionally add more manifests or addons blocks here
  }
}

output "ankra_cluster_id" {
  value = ankra_cluster.example.cluster_id
}

output "ankra_cluster_helm_command" {
  value = ankra_cluster.example.helm_command
}
