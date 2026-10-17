# An MCP token for an AI client; the value is in ankra_api_token.mcp.token.
resource "ankra_api_token" "mcp" {
  name       = "claude-desktop"
  expires_at = "2027-01-01T00:00:00Z"
  scopes     = ["mcp:read"]
}

output "mcp_token" {
  value     = ankra_api_token.mcp.token
  sensitive = true
}
