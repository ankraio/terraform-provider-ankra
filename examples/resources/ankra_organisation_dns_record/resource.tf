resource "ankra_organisation_dns_record" "logs" {
  name        = "logs"
  record_type = "CNAME"
  content     = "logs.eu-cluster.example.net"
  ttl         = 300
}
