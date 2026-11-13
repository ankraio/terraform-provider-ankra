resource "ankra_application_provenance_policy" "web" {
  application_id = ankra_application.web.id
  mode           = "enforce"
}
