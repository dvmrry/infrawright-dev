export interface RootCatalogResource {
  readonly type: string;
  readonly product: string;
  readonly provider: string;
  readonly bare_name: string;
  readonly slug_label: string | null;
  readonly generated: boolean;
  readonly derived: boolean;
}

export interface RootCatalog {
  readonly kind: "infrawright.root_catalog";
  readonly schema_version: 1;
  readonly declared_providers: readonly string[];
  readonly resources: readonly RootCatalogResource[];
  readonly source_files: readonly string[];
  readonly sources_sha256: string;
}

export interface RootProviderConfig {
  readonly strategy?: "explicit" | "slug";
  readonly groups?: Readonly<Record<string, readonly string[]>>;
  readonly bind_references?: boolean;
}

export interface Deployment {
  readonly overlay: unknown;
  readonly roots: Readonly<Record<string, RootProviderConfig>>;
}

export interface RootTopologyRoot {
  readonly label: string;
  readonly provider: string | null;
  readonly members: readonly string[];
  readonly env_dir: string | null;
}

export interface RootTopology {
  readonly kind: "infrawright.root_topology";
  readonly schema_version: 1;
  readonly tenant: string | null;
  readonly selectors: readonly string[];
  readonly directories: {
    readonly config: string;
    readonly imports: string;
    readonly envs: string;
  } | null;
  readonly roots: readonly RootTopologyRoot[];
  readonly resource_roots: Readonly<Record<string, string>>;
}

export interface WholeRootDiagnostic {
  readonly level: "note";
  readonly code: "WHOLE_ROOT_SELECTION";
  readonly message: string;
  readonly selected_members: readonly string[];
  readonly root: string;
  readonly additional_members: readonly string[];
}
