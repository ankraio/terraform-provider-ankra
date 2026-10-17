resource "ankra_ai_spend_cap" "this" {
  daily_chat_usd_cap   = 25
  daily_token_soft_cap = 2000000
  daily_token_hard_cap = 5000000
}
