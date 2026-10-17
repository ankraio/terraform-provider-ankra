# Alert routes are imported by id.
terraform import ankra_alert_route.critical_to_pager 6b1c9e7a-2d4f-4e8a-b3c5-7f9d0a1e2b3c

# A route in another project than the provider's is imported as
# <project>/<route id>, the project by ID or slug.
terraform import ankra_alert_route.team_alerts data-team/0d2e8f4a-6c1b-4a7e-9f3d-2b5c8e1a4d6f
