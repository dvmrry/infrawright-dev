terraform {
  required_version = ">= 1.0.0"
}

module "sample_groups_data" {
  source = "./data"
  items = {
    group_one = {
      state_path = "${path.module}/referent_state.json"
    }
  }
}

output "iw_reference_ids" {
  sensitive = true
  value     = module.sample_groups_data.iw_reference_ids
}
