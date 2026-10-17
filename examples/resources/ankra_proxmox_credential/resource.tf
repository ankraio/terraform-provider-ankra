resource "ankra_proxmox_credential" "lab" {
  name         = "lab"
  api_url      = "https://pve.example:8006"
  token_id     = "root@pam!ankra"
  token_secret = var.proxmox_token_secret

  jumphost = {
    host        = "bastion.example"
    private_key = file("~/.ssh/bastion")
  }

  # Changed or removed in place.
  tailscale = {
    login_server     = "https://headscale.example"
    auth_key         = var.tailscale_auth_key
    advertise_routes = "10.20.0.0/16"
  }
}

variable "proxmox_token_secret" {
  type      = string
  sensitive = true
}

variable "tailscale_auth_key" {
  type      = string
  sensitive = true
}
