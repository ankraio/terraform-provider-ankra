# The organisation card prices every private-cloud cluster whose credential has
# no card of its own.
resource "ankra_cost_rate_card" "organisation" {
  currency                  = "eur"
  vcpu_monthly_amount       = "9.50"
  memory_gb_monthly_amount  = "2.25"
  storage_gb_monthly_amount = "0.08"
}

# A card for one Proxmox, Morpheus, vSphere or MAAS credential.
resource "ankra_cost_rate_card" "lab" {
  credential_id       = "4d3c2b1a-0f9e-4d8c-b7a6-f5e4d3c2b1a0"
  currency            = "eur"
  vcpu_monthly_amount = "6"
}
