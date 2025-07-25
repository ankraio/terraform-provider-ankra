# Using the Release Build Directly in Terraform

You can use the pre-built provider binary from the [GitHub Releases page](https://github.com/ankraio/terraform-provider/releases) without any registry or dev override. Just follow these steps:

1. Download the binary for your OS and architecture from the Releases page.
2. Rename it to `terraform-provider-ankra` (remove any OS/arch suffix and file extension if present).
3. Make it executable: `chmod +x terraform-provider-ankra`
4. Place it in your Terraform plugins directory:
   - `~/.terraform.d/plugins/ankraio/ankra/ankra/0.1.0/<OS>_<ARCH>/terraform-provider-ankra`
   - Replace `<OS>` and `<ARCH>` with your system's values (e.g., `darwin_arm64`, `linux_amd64`, etc.).
5. In your Terraform configuration, use:

   ```hcl
   terraform {
     required_providers {
       ankra = {
         source  = "ankraio/ankra/ankra"
         version = ">= 0.1.0"
       }
     }
   }

   provider "ankra" {}
   ```

Terraform will automatically discover and use the local release binary. No registry publishing or dev overrides are required.

## Integrating with Existing Terraform Projects

To use the Ankra provider in your existing Terraform configuration:

1. [Install the provider](#installation) as described above.
2. Add the provider block to your Terraform code:

```hcl
terraform {
  required_providers {
    ankra = {
      source  = "ankra.io/ankra/ankra"
      version = ">= 0.1.0"
    }
  }
}

provider "ankra" {}
```

3. Use the `ankra_cluster` resource as needed in your configuration:

```hcl
resource "ankra_cluster" "example" {
  environment            = var.environment
  github_credential_name = var.github_credential_name
  github_branch          = var.github_branch
  github_repository      = var.github_repository
  ankra_token            = var.ankra_token
}
```

4. Make sure to set the required variables (e.g., via `terraform.tfvars`, environment variables, or your `.envrc` file).

5. Run `terraform init` to initialize the provider, then use `terraform plan` and `terraform apply` as usual.


# Ankra Terraform Provider

The Ankra Terraform Provider enables you to manage Ankra clusters using Terraform. It is designed for both end users and developers, with automated releases and a simple local development workflow.

---

## Overview

- **Provider Resource:** `ankra_cluster` – Manage Ankra clusters via the Ankra API.
- **Release Automation:** Binaries for Linux, macOS, and Windows are built and published automatically for each tagged release via GitHub Actions.
- **Local Development:** Supports `.envrc`/direnv for easy environment variable management and a test harness for local validation.

---

## Project Structure

- `provider/provider.go` – Main provider implementation (resource schema, create/read/delete logic, API integration).
- `.github/workflows/release.yml` – GitHub Actions workflow for building and releasing binaries on tag.
- `README.md` – Documentation, usage, and installation instructions.
- `test/project/` – Example/test Terraform project for local development (not included in release).
- `.envrc` – (Optional) Used with [direnv](https://direnv.net/) to set environment variables for local testing.

---



## Usage Example

```hcl
provider "ankra" {}

resource "ankra_cluster" "example" {
  environment            = var.environment
  github_credential_name = var.github_credential_name
  github_branch          = var.github_branch
  github_repository      = var.github_repository
  ankra_token            = var.ankra_token
}

output "ankra_cluster_id" {
  value = ankra_cluster.example.cluster_id
}
```



## Installation

### Download from GitHub Releases

1. Go to the [Releases page](https://github.com/ankraio/terraform-provider/releases) and download the binary for your OS and architecture.
2. Rename the binary to `terraform-provider-ankra` (remove any OS/arch suffix and file extension if needed).
3. Make it executable: `chmod +x terraform-provider-ankra`
4. Place it in your Terraform plugins directory:
   - For most setups: `~/.terraform.d/plugins/ankra.io/ankra/ankra/0.1.0/<OS>_<ARCH>/terraform-provider-ankra`
   - Or use the `local_provider` block in your Terraform config for local development.

### Build from Source (for developers)

```
go build -o terraform-provider-ankra
```



## Requirements
- Go 1.21+ (for building from source)
- Ankra API token (required for all usage)


## Local Development

For local development, you can use a `.envrc` file (with [direnv](https://direnv.net/)) to automatically load environment variables such as your Ankra API token. Example:

```sh
export TF_VAR_ankra_token="your-ankra-api-token"
```

This allows Terraform to pick up your token automatically when running from the `test/project` directory. See the example project for a ready-to-use test harness.


## Release Automation

Binaries are built and published automatically for each tagged release via GitHub Actions. See `.github/workflows/release.yml` for details. Customers can download the latest binaries from the [Releases page](https://github.com/ankraio/terraform-provider/releases).
