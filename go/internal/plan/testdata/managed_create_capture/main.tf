module "terraform_data" {
  source = "./managed"
  items  = var.items
}

variable "items" {
  type = map(string)
  default = {
    existing = "Existing item"
  }
}

output "iw_reference_ids" {
  sensitive = true
  value = {
    terraform_data = { for key, item in module.terraform_data.items : key => item.id }
  }
}
