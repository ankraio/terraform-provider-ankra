resource "ankra_ai_review_repository" "portal" {
  scm_provider        = "github"
  binding_external_id = "48151623"
  repo_full_name      = "acme/portal"
  ai_review           = true
  mention_replies     = true
  pr_previews         = false
  review_drafts       = true
  max_reviews_per_pr  = 3
}
