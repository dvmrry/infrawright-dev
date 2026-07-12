terraform {
  required_version = "= 1.15.4"
  required_providers {
    zcc = {
      source  = "zscaler/zcc"
      version = "= 0.1.0-beta.1"
    }
  }
}
