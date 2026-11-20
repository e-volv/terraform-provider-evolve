# Terraform provider for e-volv

Manage e-volv Launch projects, environments, flags and segments as code.

```hcl
terraform {
  required_providers {
    evolve = {
      source  = "e-volv/evolve"
      version = "~> 0.1"
    }
  }
}

provider "evolve" {
  api_key = var.evolve_launch_key # server API key with launch:manage, or EVOLVE_LAUNCH_KEY
}
```

Documentation: https://registry.terraform.io/providers/e-volv/evolve/latest/docs and https://e-volv.io/docs/flags/terraform

## Development

```bash
go test ./...
```

Releases are cut by pushing a `v*` tag; goreleaser builds, signs and publishes them.
