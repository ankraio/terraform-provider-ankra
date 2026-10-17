resource "ankra_organisation_domain" "this" {
  dns_root_domain = "k8s.example.com"
}

# Records hang off the domain, and the platform refuses to change or reset the
# domain while they exist: depending on it makes Terraform remove them first.
resource "ankra_organisation_dns_record" "logs" {
  name        = "logs"
  record_type = "CNAME"
  content     = "logs.eu-cluster.example.net"

  depends_on = [ankra_organisation_domain.this]
}
