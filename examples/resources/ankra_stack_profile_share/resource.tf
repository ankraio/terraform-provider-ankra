resource "ankra_stack_profile_share" "partner" {
  profile_id        = ankra_stack_profile.platform.id
  organisation_slug = "partner-org"
}
