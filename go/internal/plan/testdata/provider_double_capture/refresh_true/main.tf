terraform {
  required_version = ">= 1.0.0"

  required_providers {
    capture = {
      source = "infrawright/capture"
    }
  }
}

provider "capture" {}

module "capture_item" {
  source = "./data"
  items  = var.items
}

variable "items" {
  type = map(string)
  default = {
    group_one = "Location Group"
  }
}

output "iw_reference_ids" {
  sensitive = true
  value     = module.capture_item.iw_reference_ids
}
