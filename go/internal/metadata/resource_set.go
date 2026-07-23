package metadata

// ResourceDescriptor is the normalized resource identity used by topology and
// selection code. The source of truth remains LoadedPackRoot; this compact
// shape exists only for callers that construct an in-memory test or assessment
// context.
type ResourceDescriptor struct {
	Type      string
	Product   string
	Provider  string
	BareName  string
	Generated bool
	Derived   bool
}

// ResourceSet is an in-memory resource selection input. It is not persisted,
// generated, or loaded from a second metadata authority.
type ResourceSet struct {
	DeclaredProviders []string
	Resources         []ResourceDescriptor
}
