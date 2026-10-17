# A bucket the organisation already has.
resource "ankra_backup_vault" "production" {
  name              = "production-backups"
  storage_provider  = "hetzner"
  endpoint          = "https://fsn1.your-objectstorage.com"
  region            = "fsn1"
  bucket            = "acme-production-backups"
  access_key_id     = var.backup_access_key_id
  secret_access_key = var.backup_secret_access_key
}

# A bucket Ankra creates on a provider credential.
resource "ankra_backup_vault" "offsite" {
  name                       = "offsite"
  credential_id              = "5c6d7e8f-9a0b-1c2d-3e4f-5a6b7c8d9e0f"
  region                     = "europe-1"
  destroy_provider_resources = true
}

variable "backup_access_key_id" {
  type      = string
  sensitive = true
}

variable "backup_secret_access_key" {
  type      = string
  sensitive = true
}
