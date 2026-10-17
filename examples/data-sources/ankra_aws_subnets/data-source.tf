data "ankra_aws_subnets" "private" {
  credential_id = ankra_aws_role_credential.main.id
  region        = "eu-central-1"
  vpc_id        = "vpc-0123456789abcdef0"
}

locals {
  nat_subnet_ids = [for subnet in data.ankra_aws_subnets.private.subnets : subnet.id if subnet.egress_kind == "nat_gateway"]
}
