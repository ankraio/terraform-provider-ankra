variable "github_token" {
  type      = string
  sensitive = true
}

resource "ankra_secret_slot" "github" {
  label = "github-mcp-token"
  value = var.github_token
  hint  = "Read-only GitHub token for the GitHub MCP server"
}

# Reference the slot by its marker; the value never appears in the MCP
# server's configuration.
output "github_token_marker" {
  value = ankra_secret_slot.github.sentinel
}
