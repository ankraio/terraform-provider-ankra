# Helm registry credentials are imported by name. The password cannot be read
# back, so the first plan after an import shows it as a change.
terraform import ankra_helm_registry_credential.acme acme-charts

# A credential in another project than the provider's is imported as
# <project>/<name>, the project by ID or slug.
terraform import ankra_helm_registry_credential.team data-team/team-charts
