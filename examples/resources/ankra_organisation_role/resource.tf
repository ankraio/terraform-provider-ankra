resource "ankra_organisation_role" "deployer" {
  slug        = "deployer"
  name        = "Deployer"
  description = "Ships applications to the apps namespace"
  permissions = ["clusters.read", "applications.read", "applications.write"]

  kube_grants = [{
    scope     = "namespace"
    namespace = "apps"
    k8s_role  = "edit"
  }]
}
