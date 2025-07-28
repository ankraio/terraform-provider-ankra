# Ankra Terraform Provider Test Usage

This folder demonstrates how to use the local Ankra Terraform provider for development and testing.

## Prerequisites
- [Terraform](https://www.terraform.io/downloads.html) installed
- [direnv](https://direnv.net/) installed (for .envrc support)
- An Ankra API token

## Setup

1. **Set your Ankra token:**

   Edit `.envrc` in this folder and set your token:

   ```sh
   export ANKRA_TOKEN="ankra_xxx_your_token_here"
   ```
   Then run:
   ```sh
   direnv allow
   ```
   This will automatically export `ANKRA_TOKEN` in your shell when you `cd` into this folder.

2. **Configure Terraform variables:**

   The Terraform config expects a variable `ankra_token`. You can pass it from the environment:

   ```sh
   terraform apply -var="ankra_token=$ANKRA_TOKEN"
   ```
   or, for destroy:
   ```sh
   terraform destroy -var="ankra_token=$ANKRA_TOKEN"
   ```

3. **Run Terraform:**
 [https://github.com/ankraio/terraform-provider-ankra/blob/main/test/external/main.tf](https://github.com/ankraio/terraform-provider-ankra/blob/main/test/external/main.tf)

## Notes
- The provider is configured for local development using dev_overrides in `.terraformrc`.
- All debug output will be shown in the terminal for troubleshooting.
- Make sure your `ANKRA_TOKEN` is valid and has the necessary permissions.
