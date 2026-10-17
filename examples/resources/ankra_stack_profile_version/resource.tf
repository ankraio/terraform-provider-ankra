resource "ankra_stack_profile_version" "cert_manager_bump" {
  profile_id        = ankra_stack_profile.platform.id
  source_cluster_id = "3f0c6c1e-8a4b-4f39-9d55-1a2b3c4d5e6f"
  stack_name        = "platform"
  changelog         = "cert-manager 1.16"
}

resource "ankra_stack_profile_version" "withdrawn" {
  profile_id        = ankra_stack_profile.platform.id
  source_cluster_id = "3f0c6c1e-8a4b-4f39-9d55-1a2b3c4d5e6f"
  stack_name        = "platform-legacy"

  deprecation = {
    reason     = "cve"
    note       = "Upgrade to the current version."
    references = ["CVE-2026-1234"]
  }
}
