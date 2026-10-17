# Cloudflare records are imported by zone_id/record_id, or
# credential_name/zone_id/record_id when the organisation has several
# Cloudflare credentials.
terraform import ankra_cloudflare_dns_record.www cloudflare-main/023e105f4ecef8ad9ca31a8372d0c353/372e67954025e0ba6aaa6d586b9e0b59
