export interface RootCatalogResource {
  readonly type: string;
  readonly product: string;
  readonly provider: string;
  readonly bare_name: string;
  readonly slug_label: string | null;
  readonly generated: boolean;
  readonly derived: boolean;
  /** Whether automatic slug grouping may include this generated module. */
  readonly slug_group?: boolean;
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
  readonly cross_state_references?: boolean;
}

export interface Deployment {
  readonly overlay: unknown;
  readonly module_dir?: unknown;
  readonly tfvars_format?: unknown;
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

export type ChangedPathKind =
  | "config"
  | "deployment"
  | "env_root"
  | "imports"
  | "module";

export interface ChangedPathMatch {
  readonly path: string;
  readonly kinds: readonly ChangedPathKind[];
  readonly tenants: readonly string[];
  readonly resources: readonly string[];
  readonly roots: readonly string[];
}

export interface AffectedRoot {
  readonly label: string;
  readonly provider: string | null;
  readonly members: readonly string[];
  readonly matched_resources: readonly string[];
  readonly paths: readonly string[];
}

export interface ChangedPathScope {
  readonly kind: "infrawright.changed_path_scope";
  readonly schema_version: 1;
  readonly paths: readonly string[];
  readonly path_matches: readonly ChangedPathMatch[];
  readonly unmatched_paths: readonly string[];
  readonly affected_resources: readonly string[];
  readonly affected_roots: readonly AffectedRoot[];
}

export interface PlanRootArtifact {
  readonly path: string;
  readonly exists: boolean;
}

export interface MaterializedPlanRoot {
  readonly tenant: string;
  readonly label: string;
  readonly provider: string | null;
  readonly members: readonly string[];
  readonly env_dir: string;
  readonly artifact_state: "absent" | "complete" | "incomplete";
  readonly artifacts: {
    readonly tfplan: PlanRootArtifact;
    readonly tfplan_sources: PlanRootArtifact;
  };
}

export interface PlanRoots {
  readonly kind: "infrawright.plan_roots";
  readonly schema_version: 1;
  readonly request: {
    readonly tenant: string | null;
    readonly selectors: readonly string[];
  };
  readonly roots: readonly MaterializedPlanRoot[];
}
