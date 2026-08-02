terraform {
  required_version = ">= 1.0.0"

  required_providers {
    capture = {
      source = "infrawright/capture"
    }
  }
}

provider "capture" {}

# Terraform 1.15.4 reads known-input data sources during plan even with
# -refresh=false; this scenario records refresh-flag independence for the
# provider-observed evidence in this qualified configuration.
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
