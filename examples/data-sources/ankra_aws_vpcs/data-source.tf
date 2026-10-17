data "ankra_aws_vpcs" "frankfurt" {
  credential_id = ankra_aws_role_credential.main.id
  region        = "eu-central-1"
}

locals {
  default_vpc_id = one([for vpc in data.ankra_aws_vpcs.frankfurt.vpcs : vpc.id if vpc.is_default])
}
