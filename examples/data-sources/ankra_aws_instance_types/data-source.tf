data "ankra_aws_instance_types" "frankfurt" {
  credential_id = ankra_aws_role_credential.main.id
  region        = "eu-central-1"
}

locals {
  arm_types = [for type in data.ankra_aws_instance_types.frankfurt.instance_types : type.name if type.architecture == "arm64"]
}
