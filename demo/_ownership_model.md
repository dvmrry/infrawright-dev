# `$COMPANY` ownership model — the Zscaler control plane and the cloud it runs on

This repository holds **two Terraform trees with opposite organizing principles, joined by a single seam.** Understanding the split is the key to operating it — and to slicing it apart cleanly when more than one team is involved.

## Two trees, two pivots

|                  | Generated tree                       | Authored tree                                  |
| ---------------- | ------------------------------------ | ---------------------------------------------- |
| Path             | `$COMPANY/zscaler/<provider>/`       | `$COMPANY/<cloud>/<account>/<region>/<vpc>/`   |
| Pivot            | **vendor → provider**                | **cloud → account → locality → slice**         |
| Source of truth  | Zscaler control plane (fetched)      | your cloud (the running instances *are* truth) |
| Authored by      | the generator (driftless)            | you (hand-authored)                            |
| Drift            | reconciled to Zscaler                | normal Terraform drift                         |
| Credentials      | Zscaler API token                    | per-cloud / per-account IAM                    |

The generated tree is the Zscaler **configuration** (policy, connector groups, provisioning keys/URLs). The authored tree is the **infrastructure** that physically runs the connectors — and you only ever model the *slices* of each cloud you actually deploy into.

## The full shape

```
$COMPANY/                                   # adopter repo root  ($COMPANY = the tenant)
├── deployment.json                         # { "layout": "vendor-provider" }
├── _ownership_model.md                     # this file
│
├─══ GENERATED — Zscaler control plane (driftless, vendor-first) ══─
├── zscaler/
│   ├── zia/                                # internet-access policy        …abbreviated…
│   ├── zpa/                                # PRIVATE ACCESS → APP CONNECTOR control plane
│   │   ├── zpa_app_connector_group.auto.tfvars.json     # logical groups — CLOUD-AGNOSTIC
│   │   └── zpa_provisioning_key.auto.tfvars.json        # authored island (write-only secret; not fetched)
│   ├── ztc/                                # CLOUD CONNECTOR control plane
│   │   ├── ztc_provisioning_url.auto.tfvars.json        # carries cloud_provider_type = AWS|AZURE|GCP
│   │   ├── ztc_location_template.auto.tfvars.json
│   │   └── ztc_public_cloud_info.auto.tfvars.json       # links each cloud account/sub/project to Zscaler
│   ├── zcc/                                # client-connector device mgmt (NOT cloud infra)
│   ├── envs/                               # generated TF roots, one per rt
│   └── pipelines/                          # Zscaler delivery + drift pipelines
├── modules/                                # generated flat module library (machine-referenced)
│
├─══ AUTHORED — per-cloud connector INFRA (cloud-first; only the SLICES you operate) ══─
│      every leaf = its own TF root + state + creds;  byo_*=true → brownfield into networks you don't own
├── aws/
│   ├── acct-security/                      # ← ONE account you operate in  (NOT all accounts)
│   │   └── us-east-1/                      # ← ONE region                  (NOT all regions)
│   │       └── vpc-transit-prod/           # ← THE SLICE: an existing shared VPC (byo_vpc_id)
│   │           ├── ztc/                    #   Cloud Connectors (GWLB + ASG)
│   │           │   ├── main.tf             #     module {source=".../aws" byo_vpc=true byo_subnets=true byo_ngw=true}
│   │           │   ├── terraform.tfvars    #     cc_vm_prov_url=<ztc URL>  secret_name=zscaler/cc/.../prov
│   │           │   ├── outputs.tf          #     gwlb endpoint_service_name → consumed by spoke VPCs
│   │           │   └── backend.tf          #     state: aws/acct-security/us-east-1/vpc-transit-prod/ztc
│   │           └── zpa-ac/                 #   App Connectors (ASG) into the SAME slice
│   │               ├── main.tf             #     module {source="zscaler/zpa-app-connector-modules/aws" byo_vpc=true}
│   │               └── backend.tf
│   └── acct-bu-east/                       # ← a SECOND account (different team/owner)
│       └── us-east-1/vpc-bu-workload/zpa-ac/   #   this BU runs ONLY app connectors; separate state + creds
│
├── azure/
│   └── sub-corp-prod/                      # ← one subscription
│       └── rg-ztc-westeurope/              # ← the CC team's resource group (where VMs land)
│           └── vnet-hub-prod/              #   (VNet may live in a DIFFERENT rg → byo_vnet_subnets_rg_name)
│               ├── ztc/                    #   ILB + VMSS;  cc_vm_prov_url + azure_vault_url (Key Vault)
│               └── zpa-ac/
│
├── gcp/
│   └── proj-network-prod/                  # ← one project = the IAM/billing boundary
│       └── us-central1/vpc-shared-prod/    #   (Shared-VPC host project → variables.tf, not the path)
│           └── ztc/                        #   MIG + ILB, mgmt+service VPCs;  cc_vm_prov_url + gcp secret
│
└── onprem/
    └── dc-ashburn-01/ztc/                  #   OVA/VM connectors — no public cloud module, no byo_*
```

## The seam — one secret, one direction

```
zscaler/ztc/ztc_provisioning_url    ──(central team mints)──►   cloud-native secret
   (or zpa/zpa_provisioning_key)         SSM / Key Vault          zscaler/cc/<acct>/<region>/<vpc>/prov
                                                                         │
                                            cloud slice reads it ───────┘  →  cc_vm_prov_url in VM userdata
                                            (the slice never calls the Zscaler API)
```

The two trees share **no Terraform state**. The only thing that crosses the boundary is the provisioning key/URL, handed over through a cloud-native secret. The slice reads it at boot; it needs no Zscaler credentials of its own.

## Invariants

- **Slice = one VPC/VNet directory = one state file = one pipeline scope = one Zscaler connector group.** Two teams in one account are *sibling directories*, never shared state.
- **`byo_*=true` everywhere** in the authored tree: you deploy into a slice of a cloud you don't fully own; the Zscaler module builds only the connector tier and `data`-looks-up everything else (VPC, subnets, NAT, IAM).
- **No uniform region segment** — AWS `<region>`, Azure `<subscription>/<rg>/<vnet>`, GCP `<project>/<region>/<vpc>`. Each cloud is native; the `<cloud>/` grouping is a naming + routing convention, *not* a shared module or variable schema.

## It fits any shop

- **Small shop → monorepo.** Check out all of `$COMPANY/`. One repo, the generated config and every cloud slice together. This is the default; nothing special is required.
- **Larger / multi-team → slice out.** Because every slice is a self-contained Terraform root joined only by a **secret-path convention — never shared state** — any team takes only the directories it owns: the platform team takes `zscaler/`; a business unit takes just `aws/<its-account>/…`. Nobody pulls in the rest.

The secret-path convention is the decoupling contract — it lets you cleave the monorepo along any slice boundary without breaking the seam. The one move that needs a setting is putting the generated `modules/` in their own repo (a `module-source` prefix); everything else is just *which directories you check out*.

## Honest caveats

- **The cross-account secret write is governance, not layout.** The central team must write the provisioning URL+key into the *slice owner's* account. Use a named, time-scoped cross-account role agreed up front (e.g. `ZscalerProvisioningWriter`) — never a standing write permission. No directory convention solves this; it's the first thing a new slice hits.
- **Don't hard-code subnet IDs in `terraform.tfvars`.** If the VPC owner recreates a subnet, a stale ID silently breaks the next ASG scale-out. Have the VPC owner publish subnet IDs to SSM/Parameter Store; the slice reads them at plan time.
- **The drift loop covers `zscaler/` only.** Changes under `aws/`·`azure/`·`gcp/` are your team's normal Terraform responsibility — the reconcile loop does not watch them.
