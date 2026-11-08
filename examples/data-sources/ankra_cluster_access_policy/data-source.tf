data "ankra_cluster_access_policy" "this" {}

# Keep the policy honest: nobody may hold standing cluster-admin.
check "cluster_access_ceiling" {
  assert {
    condition     = data.ankra_cluster_access_policy.this.is_configured && data.ankra_cluster_access_policy.this.max_grant_role != "cluster-admin"
    error_message = "The organisation's cluster access policy still allows cluster-admin grants. Propose a lower ceiling with `ankra org access-policy set --max-role edit`."
  }
}
