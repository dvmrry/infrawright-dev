terraform {
  required_version = ">= 1.0.0"
}

module "terraform_remote_state" {
  source = "./data"
  items = {
    group_one = {
      state_path = "${path.module}/referent_state.json"
    }
  }
}

output "iw_reference_ids" {
  sensitive = true
  value     = module.terraform_remote_state.iw_reference_ids
}
