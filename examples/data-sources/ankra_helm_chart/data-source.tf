data "ankra_helm_chart" "ingress_nginx" {
  chart_name   = "ingress-nginx"
  registry_url = "https://kubernetes.github.io/ingress-nginx"
}

# Pin an ankra_cluster addon to the newest release the catalogue has indexed:
#
#   chart_version = data.ankra_helm_chart.ingress_nginx.latest_version
#   registry_name = data.ankra_helm_chart.ingress_nginx.registry_name
#   registry_url  = data.ankra_helm_chart.ingress_nginx.registry_url
