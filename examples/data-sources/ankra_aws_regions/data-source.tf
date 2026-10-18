data "ankra_aws_regions" "account" {
  credential_id = ankra_aws_role_credential.main.id
}

output "aws_regions" {
  value = data.ankra_aws_regions.account.regions[*].slug
}
