resource "ankra_mcp_tool_grant" "find_issues" {
  server_id = ankra_mcp_server.sentry.id
  tool_name = "find_issues"
  role      = "member"
}
