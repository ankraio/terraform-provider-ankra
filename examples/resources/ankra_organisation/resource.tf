# Destroy only forgets the organisation: delete it in the portal.
resource "ankra_organisation" "staging" {
  name    = "Acme Staging"
  country = "SE"
}
