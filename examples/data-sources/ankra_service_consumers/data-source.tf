data "ankra_service_consumers" "orders" {
  application_id = "3c2b1a0f-9e8d-4c7b-a6f5-e4d3c2b1a0f9"
}

output "orders_namespaces" {
  value = [for consumer in data.ankra_service_consumers.orders.consumers : consumer.namespace]
}
