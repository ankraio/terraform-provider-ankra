resource "ankra_alert_rule" "disk_pressure" {
  name              = "node-disk-pressure"
  description       = "Nodes running out of disk"
  destination_names = [ankra_alert_destination.ops.name]

  rules = [{
    name          = "disk pressure"
    resource_type = "node"
    all_clusters  = true
    severity      = "critical"
    conditions = [{
      condition_type     = "status"
      condition_operator = "equals"
      condition_value    = "DiskPressure"
    }]
  }]
}
