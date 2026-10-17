data "ankra_object_storage_buckets" "all" {}

output "bucket_endpoints" {
  value = { for bucket in data.ankra_object_storage_buckets.all.buckets : bucket.name => bucket.endpoint }
}
