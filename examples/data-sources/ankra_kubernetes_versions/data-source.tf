data "ankra_kubernetes_versions" "kubeadm" {
  distribution = "kubeadm"
}

output "stable_kubeadm_version" {
  value = data.ankra_kubernetes_versions.kubeadm.stable_version
}
