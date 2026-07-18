resource "ankra_cluster" "example" {
  cluster_name           = "dev"
  github_credential_name = "my-github-cred"
  github_branch          = "main"
  github_repository      = "ankra-io/my-repo"

  stacks {
    name        = "create-ns"
    description = "Creates a namespace"

    manifests {
      name = "test-namespace"
      manifest_base64 = base64encode(<<-YAML
        apiVersion: v1
        kind: Namespace
        metadata:
          name: test-ns
        YAML
      )
    }

    addons {
      name           = "ingress-nginx"
      chart_name     = "ingress-nginx"
      chart_version  = "4.11.0"
      repository_url = "https://kubernetes.github.io/ingress-nginx"
      namespace      = "ingress-nginx"
    }
  }
}
