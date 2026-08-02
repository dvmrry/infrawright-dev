// The provider in this directory is a test-only Terraform protocol double.
// It intentionally lives below testdata so normal engine builds do not ship
// it, while capture tests can build it explicitly and install it in a local
// filesystem mirror.
package main

import (
	"github.com/hashicorp/terraform-plugin-go/tfprotov5"
	"github.com/hashicorp/terraform-plugin-go/tfprotov5/tf5server"
)

const providerAddress = "registry.terraform.io/infrawright/capture"

func main() {
	if err := tf5server.Serve(providerAddress, func() tfprotov5.ProviderServer {
		return captureProvider{}
	}); err != nil {
		panic(err)
	}
}
