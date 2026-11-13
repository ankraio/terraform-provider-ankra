resource "ankra_application_database_backup" "web" {
  application_id = ankra_application.web.id
  enabled        = true
}
