# GENERATED smoke test — the root composes and plans against a
# mocked provider; no credentials. Regenerate: make gen-env TENANT=tenant
mock_provider "sample" {}

run "empty_plan" {
  command = plan

  variables {
    items = {}
  }
}

run "config_plan" {
  command = plan

  variables {
    items = jsondecode(file("../../../config/tenant/sample_resource.auto.tfvars.json")).items
  }
}
