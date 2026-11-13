resource "ankra_addon_definition" "observability" {
  name              = "observability"
  display_name      = "Observability stack"
  category          = "monitoring"
  match_chart_names = ["kube-prometheus-stack"]

  chart_links = [{
    chart_name   = "grafana"
    registry_url = "https://grafana.github.io/helm-charts"
    link_type    = "include"
  }]
}
