resource "ankra_object_storage_bucket" "assets" {
  name          = "app-assets"
  credential_id = "5c6d7e8f-9a0b-1c2d-3e4f-5a6b7c8d9e0f"
  region        = "europe-1"
}
