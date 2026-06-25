# Archived: Generated control-plane ownership model

This document predates the overlay/module-dir layout. It is kept as historical
design context for generated-control-plane versus authored-cloud ownership, but
its path examples are stale. Current public repo layout and keep criteria live
in [../repo-surface.md](../repo-surface.md).

---

This repository holds two Terraform surfaces with different ownership models:
generated provider control-plane artifacts and hand-authored cloud infrastructure.
The generated side has exactly one layout:

`[<overlay>/]<kind>/<tenant>/<provider>/<bare resource>`

`kind` is one of `config`, `imports`, or `envs`. `overlay` is the adopter's
free-form top-level prefix: a company name, cloud name, repo namespace, or
nothing. infrawright has no opinion above the inner `kind/tenant/provider`
structure. The demo tenant has no overlay, so it starts at repo root.

## Two Trees

|                  | Generated tree                                             | Authored tree                                  |
| ---------------- | ---------------------------------------------------------- | ---------------------------------------------- |
| Path             | `[<overlay>/]config|imports|envs/<tenant>/<provider>/`     | `[<overlay>/]<cloud>/<account>/<region>/<vpc>/` |
| Pivot            | **artifact kind -> tenant -> provider**                    | **cloud -> account -> locality -> slice**      |
| Source of truth  | provider control plane, fetched and transformed            | your cloud; running instances are truth        |
| Authored by      | the generator                                              | you                                            |
| Drift            | reconciled back to the provider control plane              | normal Terraform drift                         |
| Credentials      | provider API token                                         | per-cloud / per-account IAM                    |

The generated tree is provider configuration: policy, groups, import blocks,
and isolated env roots. The authored tree is the infrastructure that physically
runs connector workloads. You only model the cloud slices you actually deploy
into.

## Generated Shape

```
repo-root/
├── deployment.json                         # demo: {}; adopters may set {"overlay": "acme"}
├── _ownership_model.md                     # this file
│
├── config/
│   └── demo/
│       ├── zia/
│       │   ├── url_categories.auto.tfvars.json
│       │   ├── url_categories.lookup.json
│       │   └── url_filtering_rules.auto.tfvars.json
│       ├── zpa/
│       │   ├── app_connector_group.auto.tfvars.json
│       │   └── segment_group.auto.tfvars.json
│       └── zcc/
│           └── forwarding_profile.auto.tfvars.json
│
├── imports/
│   └── demo/
│       ├── zia/
│       │   └── url_categories_imports.tf
│       └── zpa/
│           └── app_connector_group_imports.tf
│
├── envs/
│   └── demo/
│       └── zpa/
│           └── app_connector_group/
│               ├── main.tf
│               └── tests/smoke.tftest.hcl
│
└── <module_dir>/                           # generated module set, keyed by full resource type
    └── zpa_app_connector_group/
```

Provider prefixes are directories. File leaves use the bare resource name:
`config/demo/zia/url_categories.auto.tfvars.json`, not
`config/demo/zia/zia_url_categories.auto.tfvars.json`. Terraform module names
and resource addresses still use the full resource type because modules are
keyed by full type.

With an overlay, the same inner tree is simply prefixed:

```
acme/config/zs3/zia/url_categories.auto.tfvars.json
acme/imports/zs3/zia/url_categories_imports.tf
acme/envs/zs3/zia/url_categories/
```

## Authored Cloud Shape

```
repo-root/
├── aws/
│   ├── acct-security/
│   │   └── us-east-1/
│   │       └── vpc-transit-prod/
│   │           ├── ztc/
│   │           │   ├── main.tf
│   │           │   ├── terraform.tfvars
│   │           │   ├── outputs.tf
│   │           │   └── backend.tf
│   │           └── zpa-ac/
│   │               ├── main.tf
│   │               └── backend.tf
│   └── acct-bu-east/
│       └── us-east-1/vpc-bu-workload/zpa-ac/
│
├── azure/
│   └── sub-corp-prod/
│       └── rg-ztc-westeurope/
│           └── vnet-hub-prod/
│               ├── ztc/
│               └── zpa-ac/
│
├── gcp/
│   └── proj-network-prod/
│       └── us-central1/vpc-shared-prod/
│           └── ztc/
│
└── onprem/
    └── dc-ashburn-01/ztc/
```

## Boundary

The generated and authored trees share no Terraform state. The only thing that
crosses the boundary is operational data such as a provisioning key or URL,
handed over through a cloud-native secret. A cloud slice reads that secret at
boot and does not need provider API credentials.

## Invariants

- Slice = one VPC/VNet directory = one state file = one pipeline scope = one connector group.
- Two teams in one account are sibling directories, never shared state.
- `byo_* = true` belongs in authored connector infrastructure when deploying into a cloud slice you do not fully own.
- Cloud paths stay cloud-native: AWS region, Azure subscription/resource-group/VNet, and GCP project/region/VPC do not need a forced common schema.
- Generated output is always `config`, `imports`, and `envs` under tenant/provider. There is no second generated shape.

## Team Splits

- Small shop: keep generated provider config and authored cloud slices in one repo.
- Larger or multi-team shop: split by ownership. The platform team owns
  generated `config/`, `imports/`, `envs/`, and the deployment-configured
  module set; business units own only their cloud slice directories.

The split works because every cloud slice is a self-contained Terraform root
joined to generated control-plane data only by secret naming and operational
handoff, never by shared state.

## Caveats

- Cross-account secret writes are governance, not layout. Use a named,
  time-scoped role agreed up front rather than standing write permission.
- Do not hard-code subnet IDs in `terraform.tfvars`. Have the VPC owner publish
  subnet IDs through the local cloud's parameter or secret store.
- The reconcile loop covers generated provider artifacts only. Cloud slice
  drift remains the owning team's normal Terraform responsibility.
