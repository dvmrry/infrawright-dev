# Schema-pin source for the cloudflare pack. `make schemas` reads the provider
# version from here and dumps schemas/provider/cloudflare.json. Exact pin (not
# ~>) so the committed schema matches the binary the Terraform run resolves —
# cloudflare/cloudflare releases on a ~2-week cadence and shifts shapes.
terraform {
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "= 5.4.0"
    }
  }
}
