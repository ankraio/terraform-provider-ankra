resource "ankra_mcp_server" "sentry" {
  name        = "sentry"
  description = "Error tracking"
  transport   = "http"
  url         = "https://mcp.sentry.dev/mcp"
  adapter_key = "sentry"

  # Sensitive values must reference a secret slot or Vault.
  env = {
    Authorization = "$${SECRET_SLOT:3f6a9c1e-0000-0000-0000-000000000000}"
  }

  allowed_tools   = ["find_issues", "get_issue_details"]
  permission_tier = "read_only"
}
