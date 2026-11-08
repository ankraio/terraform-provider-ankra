data "ankra_oidc_trust_policies" "all" {}

# Fail the plan when any policy lets pull request runs mint tokens.
check "no_pull_request_trust" {
  assert {
    condition     = alltrue([for policy in data.ankra_oidc_trust_policies.all.policies : !policy.allow_pull_request])
    error_message = "An OIDC trust policy lets pull request runs exchange tokens."
  }
}
