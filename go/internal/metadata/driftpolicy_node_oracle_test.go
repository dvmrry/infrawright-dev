package metadata

import (
	"encoding/json"
	"testing"
)

// node2415DriftPolicyRuntimeOracle is exact stdout captured on 2026-07-17.
// From the repository root, with /tmp/node_modules absent, the commands were:
//
// ln -s "$PWD/node_modules" /tmp/node_modules
// npx esbuild node-src/domain/drift-policy.ts --bundle --format=esm --external:lossless-json --outfile=/tmp/probe.mjs
// node -e 'import("file:///tmp/probe.mjs").then(({DriftPolicy})=>{const entry=(path,reason)=>({path,reason,approved_by:"a"});const run=(entries,path)=>{const p=new DriftPolicy({version:1,resource_types:{sample_resource:{plan_tolerate:entries}}});return {matched:p.toleratesPlanPath("sample_resource",path,"update"),stale:p.staleEntries({modes:["plan_tolerate"]})}};console.log(JSON.stringify({exact_first:run([entry("field[0]","exact"),entry("field[00]","alias"),entry("field[]","wildcard")],["field",0]),wildcard_first:run([entry("field[]","wildcard"),entry("field[0]","exact")],["field",0]),quoted_star_number:run([entry("quoted[\"*\"]","quoted")],["quoted",-1]),quoted_star_string:run([entry("quoted[\"*\"]","quoted")],["quoted","*"]),huge_number:run([entry("huge[9007199254740992]","huge")],["huge",9007199254740992]),segment_collision:run([entry("labels[\"x/string:y\"]","key")],["labels","x","y"]),segment_exact:run([entry("labels[\"x/string:y\"]","key")],["labels","x/string:y"])}));})'
// unlink /tmp/node_modules
//
// The mandatory external flag preserves lossless-json class identity; bundling
// that dependency makes the source's instanceof checks observe another class.
const node2415DriftPolicyRuntimeOracle = `{"exact_first":{"matched":true,"stale":[{"resource_type":"sample_resource","mode":"plan_tolerate","path":"field[00]"},{"resource_type":"sample_resource","mode":"plan_tolerate","path":"field[]"}]},"wildcard_first":{"matched":true,"stale":[{"resource_type":"sample_resource","mode":"plan_tolerate","path":"field[0]"}]},"quoted_star_number":{"matched":true,"stale":[]},"quoted_star_string":{"matched":false,"stale":[{"resource_type":"sample_resource","mode":"plan_tolerate","path":"quoted[\"*\"]"}]},"huge_number":{"matched":false,"stale":[{"resource_type":"sample_resource","mode":"plan_tolerate","path":"huge[9007199254740992]"}]},"segment_collision":{"matched":false,"stale":[{"resource_type":"sample_resource","mode":"plan_tolerate","path":"labels[\"x/string:y\"]"}]},"segment_exact":{"matched":true,"stale":[]}}`

type driftPolicyProbeResult struct {
	Matched bool               `json:"matched"`
	Stale   []StalePolicyEntry `json:"stale"`
}

type driftPolicyProbeOutput struct {
	ExactFirst       driftPolicyProbeResult `json:"exact_first"`
	WildcardFirst    driftPolicyProbeResult `json:"wildcard_first"`
	QuotedStarNumber driftPolicyProbeResult `json:"quoted_star_number"`
	QuotedStarString driftPolicyProbeResult `json:"quoted_star_string"`
	HugeNumber       driftPolicyProbeResult `json:"huge_number"`
	SegmentCollision driftPolicyProbeResult `json:"segment_collision"`
	SegmentExact     driftPolicyProbeResult `json:"segment_exact"`
}

func runDriftPolicyProbe(t *testing.T, entries []any, path []any) driftPolicyProbeResult {
	t.Helper()
	policy := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{"plan_tolerate": entries},
	})
	return driftPolicyProbeResult{
		Matched: policy.ToleratesPlanPath("sample_resource", path, "update"),
		Stale: policy.StaleEntries(StaleEntriesOptions{
			Modes: []PolicyMode{PolicyPlanTolerate},
		}),
	}
}

func TestDriftPolicyRuntimeMatchesNode2415Probe(t *testing.T) {
	entry := func(path, reason string) JsonObject {
		return runtimePolicyEntry(path, JsonObject{"reason": reason})
	}
	output := driftPolicyProbeOutput{
		ExactFirst: runDriftPolicyProbe(t, []any{
			entry("field[0]", "exact"),
			entry("field[00]", "alias"),
			entry("field[]", "wildcard"),
		}, []any{"field", 0}),
		WildcardFirst: runDriftPolicyProbe(t, []any{
			entry("field[]", "wildcard"),
			entry("field[0]", "exact"),
		}, []any{"field", 0}),
		QuotedStarNumber: runDriftPolicyProbe(t,
			[]any{entry(`quoted["*"]`, "quoted")},
			[]any{"quoted", -1},
		),
		QuotedStarString: runDriftPolicyProbe(t,
			[]any{entry(`quoted["*"]`, "quoted")},
			[]any{"quoted", "*"},
		),
		HugeNumber: runDriftPolicyProbe(t,
			[]any{entry("huge[9007199254740992]", "huge")},
			[]any{"huge", int64(9007199254740992)},
		),
		SegmentCollision: runDriftPolicyProbe(t,
			[]any{entry(`labels["x/string:y"]`, "key")},
			[]any{"labels", "x", "y"},
		),
		SegmentExact: runDriftPolicyProbe(t,
			[]any{entry(`labels["x/string:y"]`, "key")},
			[]any{"labels", "x/string:y"},
		),
	}
	rendered, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("json.Marshal(drift-policy probe output) error = %v, want nil", err)
	}
	if got := string(rendered); got != node2415DriftPolicyRuntimeOracle {
		t.Errorf("drift-policy runtime probe bytes = %s, want Node v24.15.0 bytes %s", got, node2415DriftPolicyRuntimeOracle)
	}
}
