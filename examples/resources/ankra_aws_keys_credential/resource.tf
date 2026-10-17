resource "ankra_aws_keys_credential" "main" {
  name              = "aws-keys"
  access_key_id     = "AKIAXXXXXXXXXXXXXXXX"
  secret_access_key = var.aws_secret_access_key
  region            = "eu-north-1"
}

variable "aws_secret_access_key" {
  type      = string
  sensitive = true
}
