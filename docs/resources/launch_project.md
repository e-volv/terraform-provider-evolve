# evolve_launch_project

Creates or imports a Launch project by key. Attributes: `key` and `name` (required), `description` (optional), `id` (computed). Import with `terraform import evolve_launch_project.shop shop`. A project with an environment that requires approval cannot be deleted with an API key.

# evolve_launch_environment

Nested under `project_key`. Attributes: `project_key`, `key`, `name` (required); `tone` (`neutral`, `info`, `warning` or `danger`), `critical`, `require_approval` (optional, server default when omitted); `id` (computed). An API key cannot turn `require_approval` off or delete an environment that requires approval. Import with `terraform import evolve_launch_environment.production shop/production`.

# evolve_launch_segment

Nested under `project_key`. Attributes: `project_key`, `key`, `name` (required); `description`, `context_kind`, `mode` (`rules`, `list`, `big` or `synced`), `rules_json` (optional; an empty list is sent when omitted); `id` (computed). Import with `terraform import evolve_launch_segment.beta shop/beta`.
