variable "items" {
  type = map(string)
}

resource "terraform_data" "this" {
  for_each = var.items
  input    = each.value
}

output "items" {
  value = terraform_data.this
}
