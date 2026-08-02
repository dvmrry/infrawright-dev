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

  lifecycle {
    postcondition {
      condition     = self.name == each.value
      error_message = "provider returned name does not exactly match requested name"
    }
  }
}

output "iw_reference_ids" {
  sensitive = true
  value = {
    capture_item = {
      for key, item in data.capture_item.items : key => item.id
    }
  }
}
