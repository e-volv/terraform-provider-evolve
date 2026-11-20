# evolve_launch_flag

Manages one Launch flag and, optionally, its targeting in one environment.

| Attribute                                   |          | Notes                                                                                           |
| ------------------------------------------- | -------- | ----------------------------------------------------------------------------------------------- |
| `project_key`, `key`                        | required | Changing either replaces the flag.                                                              |
| `name`, `type`                              | required | `type` is `boolean`, `string`, `number` or `json`.                                              |
| `variations_json`                           | required | The variant objects `CreateFlagDto` accepts: `key`, `value`, optional `name` and `description`. |
| `description`                               | optional |                                                                                                 |
| `client_side_available`, `mobile_available` | optional | Server default when omitted.                                                                    |
| `environment_id`                            | optional | Full environment id (`evolve_launch_environment.<name>.id`). Required with `targeting_json`.    |
| `targeting_json`                            | optional | An `UpdateTargetingDto`. Omit it when the dashboard owns targeting. Not read back from e-volv.  |
| `id`, `pending_change_id`                   | computed | `pending_change_id` is set while a targeting change awaits approval.                            |

```hcl
resource "evolve_launch_flag" "checkout" {
  project_key    = "shop"
  key            = "checkout.new"
  name           = "New checkout"
  type           = "boolean"
  environment_id = evolve_launch_environment.production.id
  variations_json = jsonencode([
    { key = "off", value = false },
    { key = "on", value = true }
  ])
  targeting_json = jsonencode({ defaultVariant = "off", rules = [] })
}
```

When the environment requires approval, a held update fails with the change request id and keeps the previously applied targeting in state; apply again after approval. A held create warns and records the requested targeting.

Import with `terraform import evolve_launch_flag.checkout shop/checkout.new`.
