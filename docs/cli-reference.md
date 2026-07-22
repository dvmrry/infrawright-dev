# Infrawright CLI reference

<!-- Code generated from the Cobra command tree. DO NOT EDIT. -->

Regenerate with `UPDATE_CLI_DOCS=1 go test ./cmd/iw -run '^TestCLIReferenceCurrent$'` from `go/`.

## `iw`

```text
Generate, adopt, assess, and apply infrastructure configuration

Usage:
  iw [flags]
  iw [command]

Available Commands:
  adopt                  Transform pulled JSON through the import oracle
  apply                  Apply exact saved Terraform plans
  assert-adoptable       Require saved plans to satisfy adoption policy
  assert-clean           Require saved plans to be clean
  check-pack             Validate pack and registry metadata
  check-pack-set         Validate an installed pack set
  clean-plans            Delete saved plan artifacts
  completion             Generate the autocompletion script for the specified shell
  deployment             Query deployment metadata
  fetch                  Fetch provider resources
  fetch-diag             Diagnose fetch TLS connectivity
  gen-env                Generate tenant environment roots
  help                   Help about any command
  modules                Generate or validate Terraform modules
  openapi-map            Map provider resources to OpenAPI operations
  plan                   Create Terraform plans
  plan-roots             Enumerate plan roots and artifacts
  provider-probe         Run the provider-readiness probe
  reconcile              Compare API JSON with a Terraform schema
  resources              List generated resources
  roots                  Emit root topology
  scope-paths            Map changed paths to affected roots
  source-evidence-eval   Evaluate source-backed provider evidence
  source-operation-map   Derive source-backed provider operation evidence
  stage-imports          Stage import and moved blocks
  transform              Transform pulled provider JSON
  transform-adopt-parity Compare Transform and Adopt fixture behavior
  unstage-imports        Remove staged import and moved blocks

Flags:
  -h, --help   help for iw

Use "iw [command] --help" for more information about a command.
```

## `iw adopt`

```text
Transform pulled JSON through the import oracle

Usage:
  iw adopt [flags]

Flags:
      --deployment string   deployment overlay path
  -h, --help                help for adopt
      --in string           input directory
      --policy string       drift or adoption policy path
      --profile string      pack profile path
      --resource string     resource selector (repeatable)
      --root string         pack root directory
      --tenant string       deployment tenant label
      --terraform string    Terraform executable path
```

## `iw apply`

```text
Apply exact saved Terraform plans

Usage:
  iw apply [flags]

Flags:
      --allow-destroy           allow a saved plan containing destroys
      --allow-non-main          allow Apply outside the configured main branch
      --allow-plan-changes      allow a saved plan containing non-import changes
      --backend-config string   Terraform backend configuration path
      --deployment string       deployment overlay path
  -h, --help                    help for apply
      --main-branch string      branch treated as the protected main branch
      --policy string           drift or adoption policy path
      --profile string          pack profile path
      --resource string         resource selector (repeatable)
      --root string             pack root directory
      --tenant string           deployment tenant label
      --terraform string        Terraform executable path
```

## `iw assert-adoptable`

```text
Require saved plans to satisfy adoption policy

Usage:
  iw assert-adoptable [flags]

Flags:
      --backend-config string   Terraform backend configuration path
      --deployment string       deployment overlay path
  -h, --help                    help for assert-adoptable
      --policy string           drift or adoption policy path
      --profile string          pack profile path
      --report string           assessment report destination or standard output
      --resource string         resource selector (repeatable)
      --root string             pack root directory
      --tenant string           deployment tenant label
      --terraform string        Terraform executable path
```

## `iw assert-clean`

```text
Require saved plans to be clean

Usage:
  iw assert-clean [flags]

Flags:
      --backend-config string   Terraform backend configuration path
      --deployment string       deployment overlay path
  -h, --help                    help for assert-clean
      --profile string          pack profile path
      --report string           assessment report destination or standard output
      --resource string         resource selector (repeatable)
      --root string             pack root directory
      --tenant string           deployment tenant label
      --terraform string        Terraform executable path
```

## `iw check-pack`

```text
Validate pack and registry metadata

Usage:
  iw check-pack [PACK=<name>] [flags]

Flags:
  -h, --help          help for check-pack
      --pack string   pack name
      --root string   pack root directory
```

## `iw check-pack-set`

```text
Validate an installed pack set

Usage:
  iw check-pack-set [flags]

Flags:
  -h, --help                  help for check-pack-set
      --profile string        pack profile path
      --requirements string   pack requirements path
      --root string           pack root directory
```

## `iw clean-plans`

```text
Delete saved plan artifacts

Usage:
  iw clean-plans [flags]

Flags:
      --deployment string   deployment overlay path
  -h, --help                help for clean-plans
      --profile string      pack profile path
      --resource string     resource selector (repeatable)
      --root string         pack root directory
      --tenant string       deployment tenant label
```

## `iw completion`

```text
Generate the autocompletion script for iw for the specified shell.
See each sub-command's help for details on how to use the generated script.

Usage:
  iw completion [command]

Available Commands:
  bash        Generate the autocompletion script for bash
  fish        Generate the autocompletion script for fish
  powershell  Generate the autocompletion script for powershell
  zsh         Generate the autocompletion script for zsh

Flags:
  -h, --help   help for completion

Use "iw completion [command] --help" for more information about a command.
```

## `iw completion bash`

```text
Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

	source <(iw completion bash)

To load completions for every new session, execute once:

#### Linux:

	iw completion bash > /etc/bash_completion.d/iw

#### macOS:

	iw completion bash > $(brew --prefix)/etc/bash_completion.d/iw

You will need to start a new shell for this setup to take effect.

Usage:
  iw completion bash

Flags:
  -h, --help              help for bash
      --no-descriptions   disable completion descriptions
```

## `iw completion fish`

```text
Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	iw completion fish | source

To load completions for every new session, execute once:

	iw completion fish > ~/.config/fish/completions/iw.fish

You will need to start a new shell for this setup to take effect.

Usage:
  iw completion fish [flags]

Flags:
  -h, --help              help for fish
      --no-descriptions   disable completion descriptions
```

## `iw completion powershell`

```text
Generate the autocompletion script for powershell.

To load completions in your current shell session:

	iw completion powershell | Out-String | Invoke-Expression

To load completions for every new session, add the output of the above command
to your powershell profile.

Usage:
  iw completion powershell [flags]

Flags:
  -h, --help              help for powershell
      --no-descriptions   disable completion descriptions
```

## `iw completion zsh`

```text
Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

	source <(iw completion zsh)

To load completions for every new session, execute once:

#### Linux:

	iw completion zsh > "${fpath[1]}/_iw"

#### macOS:

	iw completion zsh > $(brew --prefix)/share/zsh/site-functions/_iw

You will need to start a new shell for this setup to take effect.

Usage:
  iw completion zsh [flags]

Flags:
  -h, --help              help for zsh
      --no-descriptions   disable completion descriptions
```

## `iw deployment`

```text
Query deployment metadata

Usage:
  iw deployment <query> [tenant] [flags]

Flags:
      --deployment string   deployment overlay path
  -h, --help                help for deployment
```

## `iw fetch`

```text
Fetch provider resources

Usage:
  iw fetch [flags]

Flags:
      --concurrency string   maximum concurrent fetch operations
  -h, --help                 help for fetch
      --out string           output path
      --profile string       pack profile path
      --resource string      resource selector (repeatable)
      --root string          pack root directory
      --tenant string        deployment tenant label
```

## `iw fetch-diag`

```text
Diagnose fetch TLS connectivity

Usage:
  iw fetch-diag [flags]

Flags:
  -h, --help             help for fetch-diag
      --profile string   pack profile path
      --root string      pack root directory
```

## `iw gen-env`

```text
Generate tenant environment roots

Usage:
  iw gen-env [flags]

Flags:
      --backend string      Terraform backend name
      --deployment string   deployment overlay path
  -h, --help                help for gen-env
      --profile string      pack profile path
      --resource string     resource selector (repeatable)
      --root string         pack root directory
      --tenant string       deployment tenant label
```

## `iw modules`

```text
Generate or validate Terraform modules

Usage:
  iw modules [flags]
  iw modules [command]

Available Commands:
  generate    Generate Terraform modules
  validate    Validate Terraform modules

Flags:
  -h, --help   help for modules

Use "iw modules [command] --help" for more information about a command.
```

## `iw modules generate`

```text
Generate Terraform modules

Usage:
  iw modules generate [flags]

Flags:
      --deployment string   deployment overlay path
  -h, --help                help for generate
      --out string          output path
      --profile string      pack profile path
      --resource string     resource selector (repeatable)
      --root string         pack root directory
```

## `iw modules validate`

```text
Validate Terraform modules

Usage:
  iw modules validate [flags]

Flags:
      --deployment string   deployment overlay path
  -h, --help                help for validate
      --out string          output path
      --profile string      pack profile path
      --resource string     resource selector (repeatable)
      --root string         pack root directory
```

## `iw openapi-map`

```text
Map provider resources to OpenAPI operations

Usage:
  iw openapi-map [flags]

Flags:
      --api-prefix string        OpenAPI path prefix
  -h, --help                     help for openapi-map
      --openapi string           OpenAPI document path
      --out string               output path
      --provider-source string   Terraform provider source address
      --registry string          registry metadata path
      --resource-prefix string   Terraform resource-type prefix
      --schema string            Terraform provider schema path
```

## `iw plan`

```text
Create Terraform plans

Usage:
  iw plan [flags]

Flags:
      --backend-config string   Terraform backend configuration path
      --deployment string       deployment overlay path
  -h, --help                    help for plan
      --imports-only            require an import-only Terraform plan
      --profile string          pack profile path
      --resource string         resource selector (repeatable)
      --root string             pack root directory
      --save                    save the generated Terraform plan
      --tenant string           deployment tenant label
      --terraform string        Terraform executable path
```

## `iw plan-roots`

```text
Enumerate plan roots and artifacts

Usage:
  iw plan-roots [flags]

Flags:
      --deployment string   deployment overlay path
  -h, --help                help for plan-roots
      --profile string      pack profile path
      --resource string     resource selector (repeatable)
      --root string         pack root directory
      --tenant string       deployment tenant label
```

## `iw provider-probe`

```text
Run the provider-readiness probe

Usage:
  iw provider-probe <recipe.json> [flags]

Flags:
      --debug-traceback   include debug traceback details
  -h, --help              help for provider-probe
      --markdown string   Markdown summary copy destination
      --out string        output path
      --work-dir string   private provider-probe work directory
```

## `iw reconcile`

```text
Compare API JSON with a Terraform schema

Usage:
  iw reconcile <resource-type> [flags]

Flags:
      --api string               API response JSON path (repeatable where supported)
      --api-options string       API comparison options JSON path
      --fail-on-unknown          return non-zero for unknown reconciliation results
  -h, --help                     help for reconcile
      --openapi string           OpenAPI document path
      --openapi-read string      expected OpenAPI read operation
      --openapi-write string     expected OpenAPI write operation
      --out string               output path
      --override string          reconciliation override path
      --provider-source string   Terraform provider source address
      --schema string            Terraform provider schema path
```

## `iw resources`

```text
List generated resources

Usage:
  iw resources [flags]

Flags:
  -h, --help              help for resources
      --order string      resource output ordering
      --profile string    pack profile path
      --resource string   resource selector (repeatable)
      --root string       pack root directory
```

## `iw roots`

```text
Emit root topology

Usage:
  iw roots [flags]

Flags:
      --deployment string   deployment overlay path
  -h, --help                help for roots
      --profile string      pack profile path
      --resource string     resource selector (repeatable)
      --root string         pack root directory
      --tenant string       deployment tenant label
```

## `iw scope-paths`

```text
Map changed paths to affected roots

Usage:
  iw scope-paths [flags]

Flags:
      --deployment string   deployment overlay path
  -h, --help                help for scope-paths
      --path string         changed path
      --paths-json string   changed-path JSON file or standard input
      --profile string      pack profile path
      --root string         pack root directory
```

## `iw source-evidence-eval`

```text
Evaluate source-backed provider evidence

Usage:
  iw source-evidence-eval [flags]

Flags:
      --allow-unverified-source   analyze explicitly bounded source without qualified provenance
      --ast-tool-dir string       legacy AST tool directory
      --fail-on-regression        return non-zero when comparison regresses
  -h, --help                      help for source-evidence-eval
      --openapi string            OpenAPI document path
      --out-dir string            output directory
      --provider-file string      manifest-relative provider source file
      --provider-module string    provider Go module identity
      --provider-source string    Terraform provider source address
      --resource-prefix string    Terraform resource-type prefix
      --resources string          comma-separated resource filter
      --schema string             Terraform provider schema path
      --sdk-file string           module-qualified SDK source file
      --sdk-root string           SDK module and local source root
      --source-facts string       precomputed source-facts path
      --source-manifest string    qualified source manifest path
      --source-root string        provider source root directory
```

## `iw source-operation-map`

```text
Derive source-backed provider operation evidence

Usage:
  iw source-operation-map [flags]

Flags:
      --allow-unverified-source       analyze explicitly bounded source without qualified provenance
      --artifact-dir string           complete source-evidence artifact directory
      --diagnostics string            diagnostics output path
  -h, --help                          help for source-operation-map
      --openapi string                OpenAPI document path
      --out string                    output path
      --provider-file string          manifest-relative provider source file
      --provider-module string        provider Go module identity
      --provider-source string        Terraform provider source address
      --resource-prefix string        Terraform resource-type prefix
      --resources string              comma-separated resource filter
      --schema string                 Terraform provider schema path
      --sdk-file string               module-qualified SDK source file
      --sdk-root string               SDK module and local source root
      --source-facts string           precomputed source-facts path
      --source-facts-compare string   source-facts comparison path
      --source-manifest string        qualified source manifest path
      --source-root string            provider source root directory
```

## `iw stage-imports`

```text
Stage import and moved blocks

Usage:
  iw stage-imports [flags]

Flags:
      --backend-config string   Terraform backend configuration path
      --deployment string       deployment overlay path
  -h, --help                    help for stage-imports
      --profile string          pack profile path
      --resource string         resource selector (repeatable)
      --root string             pack root directory
      --state-aware             inspect local ephemeral state while staging imports
      --tenant string           deployment tenant label
      --terraform string        Terraform executable path
```

## `iw transform`

```text
Transform pulled provider JSON

Usage:
  iw transform [flags]

Flags:
      --deployment string   deployment overlay path
  -h, --help                help for transform
      --in string           input directory
      --profile string      pack profile path
      --resource string     resource selector (repeatable)
      --root string         pack root directory
      --tenant string       deployment tenant label
```

## `iw transform-adopt-parity`

```text
Compare Transform and Adopt fixture behavior

Usage:
  iw transform-adopt-parity <fixture.json> [fixture.json...] [flags]

Flags:
  -h, --help   help for transform-adopt-parity
```

## `iw unstage-imports`

```text
Remove staged import and moved blocks

Usage:
  iw unstage-imports [flags]

Flags:
      --deployment string   deployment overlay path
  -h, --help                help for unstage-imports
      --profile string      pack profile path
      --resource string     resource selector (repeatable)
      --root string         pack root directory
      --tenant string       deployment tenant label
```
