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

2. **Configure authentication:**

   The provider reads the token from the provider block or, preferably, the
   `ANKRA_TOKEN` environment variable. The API endpoint can be overridden with
   `base_url` or `ANKRA_BASE_URL`.

   ```hcl
   provider "ankra" {
     token = var.ankra_token # optional; falls back to ANKRA_TOKEN
   }
   ```

   With `ANKRA_TOKEN` exported you can simply run:

   ```sh
   terraform apply
   terraform destroy
   ```

   > The per-resource `ankra_token` attribute still works but is deprecated in
   > favour of provider-level configuration.

3. **Run Terraform:**
 [https://github.com/ankraio/terraform-provider-ankra/blob/main/test/external/main.tf](https://github.com/ankraio/terraform-provider-ankra/blob/main/test/external/main.tf)

## Releasing

Merging to `main` is the release. Ankra Pipelines (`.ankra/pipeline.yaml`) runs the tests, lint and docs check on every pull request and push. On a push to `main`, when the top section of `CHANGELOG.md` is a dated version (`## 0.3.0 (2026-10-17)`) that has no GitHub release yet, `hack/release.sh` builds, signs and publishes it with GoReleaser, and the Terraform Registry picks it up. To cut a release, open a pull request that replaces `(Unreleased)` with the date. Nobody pushes a tag; a release that failed part-way is repeated with `ankra pipeline run --ref main`, which publishes only a version that is still missing.

## Notes
- The provider is configured for local development using dev_overrides in `.terraformrc`.
- All debug output will be shown in the terminal for troubleshooting.
- Make sure your `ANKRA_TOKEN` is valid and has the necessary permissions.
