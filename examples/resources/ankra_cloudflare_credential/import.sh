# Cloudflare credentials are imported by name. The token cannot be read back;
# the first apply after the import records the configured one.
terraform import ankra_cloudflare_credential.main cloudflare-main
