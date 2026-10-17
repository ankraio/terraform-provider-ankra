resource "ankra_aws_role_credential" "provisioning" {
  name        = "aws-provisioning"
  role_arn    = "arn:aws:iam::123456789012:role/ankra-provisioning"
  external_id = var.aws_external_id
  scope       = "provisioning"
  region      = "eu-north-1"
}

# The external id the role's trust policy requires, as printed by
# `ankra credentials aws onboarding --scope provisioning`.
variable "aws_external_id" {
  type = string
}
