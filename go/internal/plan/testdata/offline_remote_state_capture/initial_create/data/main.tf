variable "items" {
  type = map(object({
    state_path = string
  }))
}

data "terraform_remote_state" "items" {
  for_each   = var.items
  backend    = "local"
  depends_on = [terraform_data.plan_dependency]
  config = {
    path = each.value.state_path
  }
  defaults = {
    id = 101
  }
}

resource "terraform_data" "plan_dependency" {
  input = "initial-create"
}

output "iw_reference_ids" {
  sensitive = true
  value = {
    terraform_remote_state = {
      for key, item in data.terraform_remote_state.items : key => item.defaults.id
    }
  }
}
