resource "ankra_security_evidence_destination" "siem" {
  name                  = "wazuh-relay"
  kind                  = "webhook"
  config                = { url = "https://siem.example.com/evidence" }
  credential_wo         = { signing_secret = var.evidence_signing_secret }
  credential_wo_version = 1
}

variable "evidence_signing_secret" {
  type      = string
  sensitive = true
  ephemeral = true
}
