# e-volv Provider

Manage e-volv Launch projects, environments, flags and segments with a server API key carrying `launch:manage`.

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
  endpoint = "https://api.e-volv.io" # or EVOLVE_API_URL
  api_key  = var.evolve_launch_key   # or EVOLVE_LAUNCH_KEY
}
```

Resources: `evolve_launch_project`, `evolve_launch_environment`, `evolve_launch_flag`, `evolve_launch_segment`. Data sources: `evolve_launch_project`, `evolve_launch_flag`.

- After every create or update the provider reads the object back from e-volv, so `id` and server defaults are known in state.
- A resource deleted outside Terraform is removed from state on the next refresh.
- JSON attributes (`variations_json`, `targeting_json`, `rules_json`) are compared by value, so formatting and key order never cause a diff.
- Resources import by key; nested resources use `<project-key>/<resource-key>`.
- Flag targeting is optional. Omit `targeting_json` to manage only metadata and variations.
- An environment that requires approval holds a targeting change and answers 202. The change request id is stored in `pending_change_id`. A held update keeps the previously applied targeting in state and fails the apply with the change id; a held create keeps the new flag, records the requested targeting and warns. A delete held for approval fails and leaves the resource in state.
