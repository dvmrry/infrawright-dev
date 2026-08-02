terraform {
  required_providers {
    capture = {
      source = "infrawright/capture"
    }
  }
}

variable "items" {
  type = map(string)
}

data "capture_item" "items" {
  for_each = var.items
  name     = each.value
}

output "iw_reference_ids" {
  sensitive = true
  value = {
    capture_item = {
      for key, item in data.capture_item.items : key => item.id
    }
  }
}
