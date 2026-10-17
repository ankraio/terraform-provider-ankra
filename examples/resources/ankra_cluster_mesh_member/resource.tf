resource "ankra_cluster_mesh" "edge" {
  name = "edge"
}

resource "ankra_cluster_mesh_member" "stockholm" {
  cluster_id = "0b6f1d2e-1111-4222-8333-944445555666"
  mesh_id    = ankra_cluster_mesh.edge.id
}
