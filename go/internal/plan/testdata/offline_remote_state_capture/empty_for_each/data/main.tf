variable "items" {
  type = map(object({
    state_path = string
  }))
}

data "terraform_remote_state" "items" {
  for_each = var.items
  backend  = "local"
  config = {
    path = each.value.state_path
  }
}

output "iw_reference_ids" {
  sensitive = true
  value = {
    terraform_remote_state = {
      for key, item in data.terraform_remote_state.items : key => item.outputs.id
    }
  }
}
