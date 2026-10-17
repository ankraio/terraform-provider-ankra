resource "ankra_ai_review_binding" "github" {
  scm_provider        = "github"
  binding_external_id = "12345678"
  ai_review           = true
  mention_replies     = true
  merge_when_green    = true
  max_reviews_per_pr  = 5
  custom_instructions = "Prefer small, focused diffs."
}
