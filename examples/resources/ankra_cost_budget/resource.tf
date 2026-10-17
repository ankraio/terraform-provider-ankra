resource "ankra_cost_budget" "organisation" {
  name          = "Whole organisation"
  scope_kind    = "organisation"
  monthly_cents = 2500000
  currency      = "eur"
}

resource "ankra_cost_budget" "production" {
  name          = "Production"
  scope_kind    = "environment"
  scope_id      = "production"
  monthly_cents = 1500000
  currency      = "eur"
  notify_at_pct = 90
  owner_user_id = "33333333-3333-3333-3333-333333333333"
}
