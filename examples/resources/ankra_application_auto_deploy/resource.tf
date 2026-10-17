resource "ankra_application_auto_deploy" "web" {
  application_id = ankra_application.web.id
  enabled        = false
}
