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

variable "output_prefix" {
  type    = string
  default = ""
}

output "iw_reference_ids" {
  sensitive = true
  value = {
    capture_item = {
      for key, id in module.capture_item.iw_reference_ids.capture_item :
      "${var.output_prefix}${key}" => id
    }
  }
}
