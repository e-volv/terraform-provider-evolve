terraform {
  required_providers {
    evolve = {
      source = "e-volv/evolve"
    }
  }
}

provider "evolve" {
  endpoint = "https://api.e-volv.io"
  api_key  = var.evolve_launch_key
}

variable "evolve_launch_key" {
  type      = string
  sensitive = true
}

resource "evolve_launch_project" "shop" {
  key  = "shop"
  name = "Shop"
}

resource "evolve_launch_environment" "production" {
  project_key = evolve_launch_project.shop.key
  key         = "production"
  name        = "Production"
}

resource "evolve_launch_flag" "checkout" {
  project_key    = evolve_launch_project.shop.key
  environment_id = evolve_launch_environment.production.id
  key            = "checkout.new"
  name           = "New checkout"
  type           = "boolean"
  variations_json = jsonencode([
    { key = "off", value = false },
    { key = "on", value = true }
  ])
}

data "evolve_launch_flag" "checkout" {
  project_key = evolve_launch_project.shop.key
  key         = evolve_launch_flag.checkout.key
}
