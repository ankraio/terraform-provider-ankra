resource "ankra_stack_profile_logo" "platform" {
  profile_id     = ankra_stack_profile.platform.id
  content_base64 = filebase64("${path.module}/logo.png")
  content_type   = "image/png"
}
