# Let the deploy workflow of acme/infra, on main and in the production
# environment, sign in to Ankra with its GitHub OIDC token:
#
#   permissions:
#     id-token: write
#   steps:
#     - run: ankra login --github-oidc --organisation-id <organisation id>
resource "ankra_oidc_trust_policy" "infra_deploy" {
  name                = "infra-deploy"
  repository_owner    = "acme"
  repository_name     = "infra"
  repository_owner_id = 1234567  # gh api users/acme --jq .id
  repository_id       = 89012345 # gh api repos/acme/infra --jq .id
  ref                 = "refs/heads/main"
  environment         = "production"
  role_slug           = "operator"
  ttl_seconds         = 900
}

# A read-only MCP token for an agent workflow, from one reusable workflow.
resource "ankra_oidc_trust_policy" "agents" {
  name                = "agents-mcp"
  repository_owner    = "acme"
  repository_name     = "agents"
  repository_owner_id = 1234567
  repository_id       = 89012399
  job_workflow_ref    = "acme/agents/.github/workflows/agent.yml@refs/heads/main"
  role_slug           = "viewer"
  scopes              = ["mcp:read"]
  ttl_seconds         = 300
}
