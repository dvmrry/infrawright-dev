terraform {
  required_providers {
    zia = {
      source  = "zscaler/zia"
      version = "4.8.0"
    }
    zpa = {
      source  = "zscaler/zpa"
      version = "4.4.6"
    }
    zcc = {
      source  = "zscaler/zcc"
      version = "0.1.0-beta.1"
    }
  }
}
