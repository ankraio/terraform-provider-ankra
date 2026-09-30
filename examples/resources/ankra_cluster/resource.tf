resource "ankra_cluster" "example" {
  cluster_name           = "dev"
  github_credential_name = "my-github-cred"
  github_branch          = "main"
  github_repository      = "ankra-io/my-repo"

  stacks {
    name        = "ingress"
    description = "Traefik ingress controller"

    manifests {
      name = "traefik-namespace"
      manifest_base64 = base64encode(<<-YAML
        apiVersion: v1
        kind: Namespace
        metadata:
          name: traefik
        YAML
      )
    }

    addons {
      name          = "traefik"
      chart_name    = "traefik"
      chart_version = "37.1.1"
      registry_name = "traefik"
      registry_url  = "https://traefik.github.io/charts"
      namespace     = "traefik"
      parents       = ["manifest:traefik-namespace"]

      configuration = <<-YAML
        deployment:
          replicas: 2
        YAML

      job_configuration = jsonencode({
        create_job_timeout = 600
        update_job_timeout = 600
      })
    }
  }

  timeouts {
    create = "30m"
    update = "20m"
  }
}
