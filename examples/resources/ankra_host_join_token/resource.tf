resource "ankra_host_join_token" "kiosk" {
  environment = "edge"
  ttl_seconds = 3600
}

# Hand the token to the host's first boot, for example from cloud-init:
#   echo "$JOIN_TOKEN" | ankra targets register --environment edge --name kiosk-1 --token-stdin
output "kiosk_join_token" {
  value     = ankra_host_join_token.kiosk.join_token
  sensitive = true
}
