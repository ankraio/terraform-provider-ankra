# DNS credentials are imported by name. The webhook URL cannot be read back;
# the first apply after the import records the configured one.
terraform import ankra_dns_credential.webhook corporate-dns
