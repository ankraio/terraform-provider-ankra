# Helm registries are imported by name.
terraform import ankra_helm_registry.acme acme

# A registry in another project than the provider's is imported as
# <project>/<name>, the project by ID or slug.
terraform import ankra_helm_registry.team data-team/team
