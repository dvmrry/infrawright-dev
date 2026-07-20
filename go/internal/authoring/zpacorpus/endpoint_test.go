package zpacorpus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceanalysis"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

const (
	endpointManifestSHA256 = "577eaf74544f0d24a52205a13922ca4cc3803701cfda7557da6367e68ead55bc"
	endpointInputSHA256    = "cf6c9945d33f99756bbdfdce78e3f8f122b9cf6c81445a58517b7cac216d465e"
	endpointReportSHA256   = "4897d20a680c433473b34459c885c20c2067de12c860640b3730111bbd279039"
	endpointProviderCommit = "dcf12469a9a8f648be0691c74e9816fc94ec7ddc"
	endpointProviderTree   = "78fda0a980f34f051b7f30c3dd413921099d10144834542d7810dba87de6ed7c"
	endpointSDKModule      = "github.com/zscaler/zscaler-sdk-go/v3"
	endpointSDKVersion     = "v3.8.40"
	endpointSDKTree        = "bb09df1ca7ab79c3949f228040cc8c01757cb6cd8c0f315e2f3b494ea22e0392"
)

var endpointResourceTypes = []string{
	"zpa_app_connector_group",
	"zpa_application_segment",
	"zpa_application_server",
	"zpa_ba_certificate",
	"zpa_emergency_access_user",
	"zpa_inspection_custom_controls",
	"zpa_inspection_profile",
	"zpa_microtenant_controller",
	"zpa_policy_access_rule",
	"zpa_pra_approval_controller",
	"zpa_pra_console_controller",
	"zpa_pra_credential_controller",
	"zpa_pra_portal_controller",
	"zpa_segment_group",
	"zpa_server_group",
	"zpa_service_edge_group",
}

type endpointRowAuthority struct {
	constructor        string
	registrationLine   int
	providerFile       string
	readCallback       string
	readLine           int
	providerCallLine   int
	providerCallColumn int
	sdkPackage         string
	sdkSymbol          string
	sdkFile            string
	sdkLine            int
}

// endpointRows was transcribed from the exact pinned source anchors. It is an
// independent check on the committed report, not analyzer-produced data.
var endpointRows = map[string]endpointRowAuthority{
	"zpa_app_connector_group": {
		constructor: "resourceAppConnectorGroup", registrationLine: 148,
		providerFile: "zpa/resource_zpa_app_connector_group.go", readCallback: "resourceAppConnectorGroupRead", readLine: 269,
		providerCallLine: 280, providerCallColumn: 36, sdkPackage: "appconnectorgroup", sdkSymbol: "GetAll",
		sdkFile: "zscaler/zpa/services/appconnectorgroup/zpa_app_connector_group.go", sdkLine: 165,
	},
	"zpa_application_segment": {
		constructor: "resourceApplicationSegment", registrationLine: 150,
		providerFile: "zpa/resource_zpa_application_segment.go", readCallback: "resourceApplicationSegmentRead", readLine: 326,
		providerCallLine: 335, providerCallColumn: 37, sdkPackage: "applicationsegment", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/applicationsegment/zpa_application_segment.go", sdkLine: 151,
	},
	"zpa_application_server": {
		constructor: "resourceApplicationServer", registrationLine: 149,
		providerFile: "zpa/resource_zpa_app_server_controller.go", readCallback: "resourceApplicationServerRead", readLine: 126,
		providerCallLine: 135, providerCallColumn: 38, sdkPackage: "appservercontroller", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/appservercontroller/zpa_app_server_controller.go", sdkLine: 33,
	},
	"zpa_ba_certificate": {
		constructor: "resourceBaCertificate", registrationLine: 156,
		providerFile: "zpa/resource_zpa_ba_certificate.go", readCallback: "resourceBaCertificateRead", readLine: 82,
		providerCallLine: 91, providerCallColumn: 32, sdkPackage: "bacertificate", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/bacertificate/zpa_ba_certificate.go", sdkLine: 42,
	},
	"zpa_emergency_access_user": {
		constructor: "resourceEmergencyAccess", registrationLine: 160,
		providerFile: "zpa/resource_zpa_emergency_access.go", readCallback: "resourceEmergencyAccessRead", readLine: 73,
		providerCallLine: 82, providerCallColumn: 34, sdkPackage: "emergencyaccess", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/emergencyaccess/emergencyaccess.go", sdkLine: 34,
	},
	"zpa_inspection_custom_controls": {
		constructor: "resourceInspectionCustomControls", registrationLine: 183,
		providerFile: "zpa/resource_zpa_inspection_custom_controls.go", readCallback: "resourceInspectionCustomControlsRead", readLine: 308,
		providerCallLine: 312, providerCallColumn: 45, sdkPackage: "inspection_custom_controls", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/inspectioncontrol/inspection_custom_controls/zpa_inspection_custom_controls.go", sdkLine: 60,
	},
	"zpa_inspection_profile": {
		constructor: "resourceInspectionProfile", registrationLine: 184,
		providerFile: "zpa/resource_zpa_inspection_profile.go", readCallback: "resourceInspectionProfileRead", readLine: 293,
		providerCallLine: 297, providerCallColumn: 37, sdkPackage: "inspection_profile", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/inspectioncontrol/inspection_profile/zpa_inspection_profile.go", sdkLine: 133,
	},
	"zpa_microtenant_controller": {
		constructor: "resourceMicrotenantController", registrationLine: 185,
		providerFile: "zpa/resource_zpa_microtenant_controller.go", readCallback: "resourceMicrotenantRead", readLine: 133,
		providerCallLine: 137, providerCallColumn: 31, sdkPackage: "microtenants", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/microtenants/microtenants.go", sdkLine: 97,
	},
	"zpa_pra_approval_controller": {
		constructor: "resourcePRAPrivilegedApprovalController", registrationLine: 186,
		providerFile: "zpa/resource_zpa_pra_approval.go", readCallback: "resourcePRAPrivilegedApprovalControllerRead", readLine: 206,
		providerCallLine: 215, providerCallColumn: 30, sdkPackage: "praapproval", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/privilegedremoteaccess/praapproval/praapproval.go", sdkLine: 95,
	},
	"zpa_pra_console_controller": {
		constructor: "resourcePRAConsoleController", registrationLine: 190,
		providerFile: "zpa/resource_zpa_pra_console_controller.go", readCallback: "resourcePRAConsoleControllerRead", readLine: 143,
		providerCallLine: 152, providerCallColumn: 29, sdkPackage: "praconsole", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/privilegedremoteaccess/praconsole/praconsole.go", sdkLine: 63,
	},
	"zpa_pra_credential_controller": {
		constructor: "resourcePRACredentialController", registrationLine: 188,
		providerFile: "zpa/resource_zpa_pra_credential_controller.go", readCallback: "resourcePRACredentialControllerRead", readLine: 140,
		providerCallLine: 149, providerCallColumn: 32, sdkPackage: "pracredential", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/privilegedremoteaccess/pracredential/credential_controller.go", sdkLine: 73,
	},
	"zpa_pra_portal_controller": {
		constructor: "resourcePRAPortalController", registrationLine: 187,
		providerFile: "zpa/resource_zpa_pra_portal_controller.go", readCallback: "resourcePRAPortalControllerRead", readLine: 181,
		providerCallLine: 190, providerCallColumn: 28, sdkPackage: "praportal", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/privilegedremoteaccess/praportal/praportal.go", sdkLine: 97,
	},
	"zpa_segment_group": {
		constructor: "resourceSegmentGroup", registrationLine: 161,
		providerFile: "zpa/resource_zpa_segment_group.go", readCallback: "resourceSegmentGroupRead", readLine: 117,
		providerCallLine: 134, providerCallColumn: 31, sdkPackage: "segmentgroup", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/segmentgroup/zpa_segment_group.go", sdkLine: 81,
	},
	"zpa_server_group": {
		constructor: "resourceServerGroup", registrationLine: 162,
		providerFile: "zpa/resource_zpa_server_group.go", readCallback: "resourceServerGroupRead", readLine: 226,
		providerCallLine: 235, providerCallColumn: 30, sdkPackage: "servergroup", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/servergroup/zpa_server_group.go", sdkLine: 130,
	},
	"zpa_service_edge_group": {
		constructor: "resourceServiceEdgeGroup", registrationLine: 180,
		providerFile: "zpa/resource_zpa_service_edge_group.go", readCallback: "resourceServiceEdgeGroupRead", readLine: 313,
		providerCallLine: 322, providerCallColumn: 35, sdkPackage: "serviceedgegroup", sdkSymbol: "Get",
		sdkFile: "zscaler/zpa/services/serviceedgegroup/zpa_service_edge_group.go", sdkLine: 64,
	},
}

var endpointSDKRequestLines = map[string]int{
	"zpa_application_segment":        154,
	"zpa_application_server":         36,
	"zpa_ba_certificate":             45,
	"zpa_emergency_access_user":      37,
	"zpa_inspection_custom_controls": 63,
	"zpa_inspection_profile":         136,
	"zpa_microtenant_controller":     100,
	"zpa_pra_approval_controller":    98,
	"zpa_pra_console_controller":     66,
	"zpa_pra_credential_controller":  76,
	"zpa_pra_portal_controller":      100,
	"zpa_segment_group":              84,
	"zpa_server_group":               133,
	"zpa_service_edge_group":         67,
}

func TestEndpointFixtureManifestIsCanonicalAndPinned(t *testing.T) {
	data, manifest := readEndpointManifest(t)
	if got := digest(data); got != endpointManifestSHA256 {
		t.Fatalf("digest(endpoint manifest) = %q, want %q", got, endpointManifestSHA256)
	}
	rendered, err := contracts.RenderSourceProvenance(manifest)
	if err != nil {
		t.Fatalf("contracts.RenderSourceProvenance(endpoint manifest) error = %v, want nil", err)
	}
	if !bytes.Equal([]byte(rendered), data) {
		t.Error("contracts.RenderSourceProvenance(endpoint manifest) differs from committed canonical bytes")
	}

	if manifest.Provider.Revision != endpointProviderCommit {
		t.Errorf("endpoint manifest provider revision = %q, want %q", manifest.Provider.Revision, endpointProviderCommit)
	}
	if manifest.Provider.TreeSHA256 != endpointProviderTree {
		t.Errorf("endpoint manifest provider tree = %q, want %q", manifest.Provider.TreeSHA256, endpointProviderTree)
	}
	if len(manifest.SDKs) != 1 {
		t.Fatalf("len(endpoint manifest SDKs) = %d, want 1", len(manifest.SDKs))
	}
	sdk := manifest.SDKs[0]
	if sdk.ModulePath != endpointSDKModule || sdk.ModuleVersion != endpointSDKVersion {
		t.Errorf("endpoint manifest SDK identity = %q@%q, want %q@%q", sdk.ModulePath, sdk.ModuleVersion, endpointSDKModule, endpointSDKVersion)
	}
	if sdk.Revision != nil {
		t.Errorf("endpoint manifest SDK revision = %q, want nil for module-cache binding", *sdk.Revision)
	}
	if sdk.TreeSHA256 == nil || *sdk.TreeSHA256 != endpointSDKTree {
		t.Errorf("endpoint manifest SDK tree = %v, want %q", sdk.TreeSHA256, endpointSDKTree)
	}
	if !reflect.DeepEqual(manifest.Selection.ResourceTypes, endpointResourceTypes) {
		t.Errorf("endpoint manifest resource types = %v, want %v", manifest.Selection.ResourceTypes, endpointResourceTypes)
	}
	if len(manifest.Selection.Filters) != 0 {
		t.Errorf("len(endpoint manifest filters) = %d, want 0", len(manifest.Selection.Filters))
	}
	if manifest.OpenAPI != nil {
		t.Error("endpoint manifest OpenAPI binding is non-nil, want source-only fixture")
	}
	if len(manifest.UnavailableSDKs) != 0 {
		t.Errorf("len(endpoint manifest unavailable SDKs) = %d, want 0", len(manifest.UnavailableSDKs))
	}

	providerBindings := append([]contracts.FileBinding(nil), manifest.Provider.Files...)
	providerBindings = append(providerBindings, manifest.ProviderModule.GoMod, *manifest.ProviderModule.GoSum)
	if got := bindingTreeDigest(providerBindings); got != manifest.Provider.TreeSHA256 {
		t.Errorf("bindingTreeDigest(endpoint provider) = %q, want manifest tree %q", got, manifest.Provider.TreeSHA256)
	}
	if got := bindingTreeDigest(sdk.Files); got != *sdk.TreeSHA256 {
		t.Errorf("bindingTreeDigest(endpoint SDK) = %q, want manifest tree %q", got, *sdk.TreeSHA256)
	}

	providerPaths := bindingPaths(manifest.Provider.Files)
	for _, path := range []string{"main.go", "zpa/common.go", "zpa/config.go", "zpa/provider.go"} {
		if _, exists := providerPaths[path]; !exists {
			t.Errorf("endpoint provider binding lacks required source %q", path)
		}
	}
	sdkPaths := bindingPaths(sdk.Files)
	for _, path := range []string{"go.mod", "zscaler/service.go", "zscaler/zparequests.go", "zscaler/zpa/services/common/common.go"} {
		if _, exists := sdkPaths[path]; !exists {
			t.Errorf("endpoint SDK binding lacks required source %q", path)
		}
	}
	if _, exists := sdkPaths["zscaler/oneapiconfig.go"]; exists {
		t.Error("endpoint SDK binding contains zscaler/oneapiconfig.go, outside the bounded A1 direct-sink proof")
	}
}

func TestEndpointFixtureLocalSchemaBinding(t *testing.T) {
	_, manifest := readEndpointManifest(t)
	filename := filepath.Join(repositoryRoot(t), filepath.FromSlash(manifest.TerraformSchema.Path))
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("os.ReadFile(endpoint fixture schema %q) error = %v", filename, err)
	}
	if got := digest(data); got != manifest.TerraformSchema.SHA256 {
		t.Errorf("digest(endpoint fixture schema %q) = %q, want %q", filename, got, manifest.TerraformSchema.SHA256)
	}
}

func TestEndpointFixtureExpectedAuthorityIsCanonicalAndSourceAnchored(t *testing.T) {
	manifestData, manifest := readEndpointManifest(t)
	reportData, report := readEndpointReport(t)
	if got := digest(reportData); got != endpointReportSHA256 {
		t.Fatalf("digest(endpoint expected report) = %q, want %q", got, endpointReportSHA256)
	}
	rendered, err := contracts.RenderSourceEvidenceReport(report)
	if err != nil {
		t.Fatalf("contracts.RenderSourceEvidenceReport(endpoint authority) error = %v, want nil", err)
	}
	if !bytes.Equal([]byte(rendered), reportData) {
		t.Error("contracts.RenderSourceEvidenceReport(endpoint authority) differs from committed canonical bytes")
	}

	manifestSHA := digest(manifestData)
	input := contracts.InputProvenance{
		Kind:                 "infrawright.input_provenance",
		SchemaVersion:        1,
		SourceTrust:          contracts.SourceTrustVerified,
		SourceManifestSHA256: &manifestSHA,
		SourceManifest:       &manifest,
	}
	inputBytes, err := contracts.RenderInputProvenance(input)
	if err != nil {
		t.Fatalf("contracts.RenderInputProvenance(endpoint authority) error = %v, want nil", err)
	}
	if got := digest([]byte(inputBytes)); got != endpointInputSHA256 {
		t.Fatalf("digest(endpoint input provenance) = %q, want %q", got, endpointInputSHA256)
	}
	if err := contracts.ValidateSourceEvidenceReportAgainstInput(report, input); err != nil {
		t.Fatalf("contracts.ValidateSourceEvidenceReportAgainstInput(endpoint authority) error = %v, want nil", err)
	}

	for resourceType, authority := range endpointRows {
		row, exists := report.Resources[resourceType]
		if !exists {
			t.Errorf("endpoint authority lacks resource %q", resourceType)
			continue
		}
		assertEndpointRowAuthority(t, resourceType, row, authority)
	}
	assertPolicyEndpointAuthority(t, report.Resources["zpa_policy_access_rule"])
	if report.Summary.SelectedTotal != 16 || report.Summary.ApplicableTotal != 16 || report.Summary.SourceCallObservedTotal != 15 || report.Summary.EndpointObservedTotal != 0 {
		t.Errorf("endpoint authority summary = %+v, want selected/applicable/source-call/endpoint 16/16/15/0", report.Summary)
	}
	counts := report.Summary.ClassificationCounts
	if counts.ObservedSDKCall != 15 || counts.Ambiguous != 1 || counts.ObservedHTTP != 0 || counts.Dynamic != 0 || counts.Unresolved != 0 || counts.NoSource != 0 || counts.NotApplicable != 0 {
		t.Errorf("endpoint authority classification counts = %+v, want 15 observed_sdk_call and 1 ambiguous only", counts)
	}
}

func TestEndpointFixtureOptionalExternalBindings(t *testing.T) {
	providerRoot, providerSet := os.LookupEnv("ZPA_PROVIDER_SOURCE")
	sdkRoot, sdkSet := os.LookupEnv("ZPA_SDK_SOURCE")
	if !providerSet && !sdkSet {
		t.Skip("ZPA_PROVIDER_SOURCE and ZPA_SDK_SOURCE are unset")
	}
	if !providerSet || !sdkSet || providerRoot == "" || sdkRoot == "" {
		t.Fatal("ZPA_PROVIDER_SOURCE and ZPA_SDK_SOURCE must both be non-empty when either is set")
	}
	loaded, err := sourcebind.LoadVerified(context.Background(), sourcebind.LocalRoots{
		ManifestPath: endpointManifestPath(t),
		ProviderRoot: providerRoot,
		SDKRoots:     map[string]string{endpointSDKModule: sdkRoot},
		SchemaRoot:   repositoryRoot(t),
	})
	if err != nil {
		t.Fatalf("sourcebind.LoadVerified(endpoint fixture roots) error = %v, want nil", err)
	}
	inputs, err := sourcebind.RequireQualification(loaded)
	if err != nil {
		t.Fatalf("sourcebind.RequireQualification(endpoint fixture roots) error = %v, want nil", err)
	}
	got, err := sourceanalysis.Analyze(context.Background(), inputs)
	if err != nil {
		t.Fatalf("sourceanalysis.Analyze(endpoint fixture roots) error = %v, want nil", err)
	}
	gotBytes, err := got.CanonicalBytes()
	if err != nil {
		t.Fatalf("QualifiedEvidence.CanonicalBytes(endpoint fixture roots) error = %v, want nil", err)
	}
	wantBytes, want := readEndpointReport(t)
	if !bytes.Equal(gotBytes, wantBytes) {
		gotReport, snapshotErr := got.Snapshot()
		if snapshotErr != nil {
			t.Fatalf("QualifiedEvidence.Snapshot(endpoint fixture roots) error = %v", snapshotErr)
		}
		t.Errorf("sourceanalysis.Analyze(endpoint fixture roots) differs from hand-authored authority: got SHA-256 %s and summary %+v; want SHA-256 %s and summary %+v", digest(gotBytes), gotReport.Summary, endpointReportSHA256, want.Summary)
	}
}

func readEndpointManifest(t *testing.T) ([]byte, contracts.SourceProvenance) {
	t.Helper()
	filename := endpointManifestPath(t)
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("os.ReadFile(endpoint manifest %q) error = %v", filename, err)
	}
	manifest, err := contracts.DecodeSourceProvenance(data)
	if err != nil {
		t.Fatalf("contracts.DecodeSourceProvenance(endpoint manifest) error = %v", err)
	}
	return data, manifest
}

func endpointManifestPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repositoryRoot(t), "tests", "fixtures", "authoring", "zpa-v4.4.6-endpoint-v1", "source-provenance-v1.json")
}

func readEndpointReport(t *testing.T) ([]byte, contracts.SourceEvidenceReport) {
	t.Helper()
	filename := filepath.Join(repositoryRoot(t), "tests", "fixtures", "authoring", "zpa-v4.4.6-endpoint-v1", "expected", "source-evidence-report-v1.json")
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("os.ReadFile(endpoint report %q) error = %v", filename, err)
	}
	report, err := contracts.DecodeSourceEvidenceReport(data)
	if err != nil {
		t.Fatalf("contracts.DecodeSourceEvidenceReport(endpoint report) error = %v", err)
	}
	return data, report
}

func assertEndpointRowAuthority(t *testing.T, resourceType string, row contracts.SourceEvidenceRow, authority endpointRowAuthority) {
	t.Helper()
	if row.Classification != contracts.SourceObservedSDKCall || row.LegacyMapped || row.ReasonCode != nil {
		t.Errorf("endpoint authority row %q state = (%q,%t,%v), want observed_sdk_call,false,nil", resourceType, row.Classification, row.LegacyMapped, row.ReasonCode)
	}
	if row.ProviderRegistration == nil || row.ProviderRegistration.Symbol != authority.constructor || row.ProviderRegistration.Location.Path != "zpa/provider.go" || row.ProviderRegistration.Location.Line != authority.registrationLine || row.ProviderRegistration.Location.Column != 52 {
		t.Errorf("endpoint authority row %q registration = %+v, want %s at zpa/provider.go:%d:52", resourceType, row.ProviderRegistration, authority.constructor, authority.registrationLine)
	}
	if row.ReadCallback == nil || row.ReadCallback.Symbol != authority.readCallback || row.ReadCallback.Location.Path != authority.providerFile || row.ReadCallback.Location.Line != authority.readLine || row.ReadCallback.Location.Column != 6 {
		t.Errorf("endpoint authority row %q Read callback = %+v, want %s at %s:%d:6", resourceType, row.ReadCallback, authority.readCallback, authority.providerFile, authority.readLine)
	}
	if len(row.Chains) != 1 {
		t.Errorf("len(endpoint authority row %q chains) = %d, want 1", resourceType, len(row.Chains))
		return
	}
	assertSDKChainAuthority(t, resourceType, row.Chains[0], authority, authority.readCallback, authority.providerFile)
}

func assertSDKChainAuthority(t *testing.T, label string, chain contracts.SourceEvidenceChain, authority endpointRowAuthority, callerName, callerFile string) {
	t.Helper()
	if chain.Endpoint != nil || chain.SDKCall == nil || chain.ReasonCode == nil || *chain.ReasonCode != contracts.ReasonEndpointNotRecovered {
		t.Errorf("endpoint authority chain %q terminal = endpoint:%v SDK:%v reason:%v, want SDK endpoint_not_recovered", label, chain.Endpoint, chain.SDKCall, chain.ReasonCode)
		return
	}
	wantPackage := endpointSDKModule + "/" + path.Dir(authority.sdkFile)
	if chain.SDKCall.ModulePath != endpointSDKModule || chain.SDKCall.ModuleVersion != endpointSDKVersion || chain.SDKCall.PackagePath != wantPackage || chain.SDKCall.Symbol != authority.sdkSymbol || chain.SDKCall.Location.Path != authority.sdkFile || chain.SDKCall.Location.Line != authority.sdkLine || chain.SDKCall.Location.Column != 6 {
		t.Errorf("endpoint authority chain %q SDK = %+v, want %s at %s:%d:6", label, chain.SDKCall, authority.sdkSymbol, authority.sdkFile, authority.sdkLine)
	}
	wantSteps := 1
	requestLine, hasRequest := endpointSDKRequestLines[label]
	if hasRequest {
		wantSteps++
	}
	if len(chain.Steps) != wantSteps {
		t.Errorf("len(endpoint authority chain %q steps) = %d, want %d", label, len(chain.Steps), wantSteps)
		return
	}
	step := chain.Steps[0]
	if step.Kind != contracts.CallSDKPackageFunction || step.ImportPath == nil || *step.ImportPath != wantPackage || step.Callee == nil || step.Callee.PackagePath != wantPackage || step.Callee.Symbol != authority.sdkSymbol || step.Symbol != authority.sdkPackage+"."+authority.sdkSymbol || step.Location.Path != callerFile || step.Location.Function == nil || *step.Location.Function != callerName || step.Location.Line != authority.providerCallLine || step.Location.Column != authority.providerCallColumn {
		t.Errorf("endpoint authority chain %q provider call = %+v, want %s.%s at %s:%d:%d", label, step, authority.sdkPackage, authority.sdkSymbol, callerFile, authority.providerCallLine, authority.providerCallColumn)
	}
	if hasRequest {
		assertSDKRequestStepAuthority(t, label, chain.Steps[1], authority.sdkSymbol, authority.sdkFile, requestLine)
	}
	for _, candidate := range chain.Steps {
		if candidate.Kind == contracts.CallRawHTTP {
			t.Errorf("endpoint authority chain %q contains raw_http, want no uncertified HTTP claim", label)
		}
	}
}

func assertPolicyEndpointAuthority(t *testing.T, row contracts.SourceEvidenceRow) {
	t.Helper()
	if row.Classification != contracts.SourceAmbiguous || row.LegacyMapped || row.ReasonCode == nil || *row.ReasonCode != contracts.ReasonMultipleCandidates || len(row.Chains) != 2 {
		t.Errorf("policy endpoint authority state = (%q,%t,%v,%d chains), want ambiguous,false,multiple_viable_candidates,2", row.Classification, row.LegacyMapped, row.ReasonCode, len(row.Chains))
		return
	}
	wantSymbols := []string{"GetByPolicyType", "GetPolicyRule"}
	for index, chain := range row.Chains {
		if chain.Endpoint != nil || chain.SDKCall == nil || chain.SDKCall.Symbol != wantSymbols[index] || chain.ReasonCode == nil || *chain.ReasonCode != contracts.ReasonEndpointNotRecovered {
			t.Errorf("policy endpoint authority chain[%d] = %+v, want %s endpoint_not_recovered", index, chain, wantSymbols[index])
		}
	}
	if len(row.Chains[0].Steps) != 3 || row.Chains[0].Steps[0].Kind != contracts.CallProviderHelper || row.Chains[0].Steps[0].Symbol != "fetchPolicySetIDByType" || row.Chains[0].Steps[0].Location.Line != 139 || row.Chains[0].Steps[1].Kind != contracts.CallSDKPackageFunction || row.Chains[0].Steps[1].Symbol != "policysetcontroller.GetByPolicyType" || row.Chains[0].Steps[1].Location.Path != "zpa/common.go" || row.Chains[0].Steps[1].Location.Line != 1269 || row.Chains[0].Steps[1].Location.Column != 49 {
		t.Errorf("policy endpoint authority prerequisite chain = %+v, want Read→fetchPolicySetIDByType→policysetcontroller.GetByPolicyType exact anchors", row.Chains[0].Steps)
	} else {
		assertSDKRequestStepAuthority(t, "zpa_policy_access_rule prerequisite", row.Chains[0].Steps[2], "GetByPolicyType", "zscaler/zpa/services/policysetcontroller/policysetcontroller.go", 153)
	}
	if len(row.Chains[1].Steps) != 2 || row.Chains[1].Steps[0].Symbol != "policysetcontroller.GetPolicyRule" || row.Chains[1].Steps[0].Location.Line != 145 || row.Chains[1].Steps[0].Location.Column != 38 {
		t.Errorf("policy endpoint authority object chain = %+v, want direct policysetcontroller.GetPolicyRule at line 145:38", row.Chains[1].Steps)
	} else {
		assertSDKRequestStepAuthority(t, "zpa_policy_access_rule object", row.Chains[1].Steps[1], "GetPolicyRule", "zscaler/zpa/services/policysetcontroller/policysetcontroller.go", 166)
	}
}

func assertSDKRequestStepAuthority(t *testing.T, label string, step contracts.SourceCallStep, callerSymbol, callerFile string, callLine int) {
	t.Helper()
	wantClientPackage := endpointSDKModule + "/zscaler"
	wantCallerPackage := endpointSDKModule + "/" + path.Dir(callerFile)
	wantSymbol := "(*zscaler.Client).NewRequestDo"
	if step.Kind != contracts.CallSDKReceiverMethod || step.Symbol != wantSymbol || step.ImportPath == nil || *step.ImportPath != wantClientPackage {
		t.Errorf("endpoint authority chain %q terminal request kind = (%q,%q,%v), want sdk_receiver_method,%q,%q", label, step.Kind, step.Symbol, step.ImportPath, wantSymbol, wantClientPackage)
	}
	if step.Location.Origin != contracts.SourceLocationSDK || step.Location.SDKModulePath == nil || *step.Location.SDKModulePath != endpointSDKModule || step.Location.Path != callerFile || step.Location.Function == nil || *step.Location.Function != callerSymbol || step.Location.Line != callLine || step.Location.Column != 30 {
		t.Errorf("endpoint authority chain %q terminal request call = %+v, want %s at %s:%d:30", label, step.Location, wantSymbol, callerFile, callLine)
	}
	if step.Caller.PackagePath != wantCallerPackage || step.Caller.Symbol != callerSymbol {
		t.Errorf("endpoint authority chain %q terminal request caller = %+v, want %s.%s", label, step.Caller, wantCallerPackage, callerSymbol)
	}
	if step.Callee == nil || step.Callee.PackagePath != wantClientPackage || step.Callee.Symbol != "(*Client).NewRequestDo" || step.Callee.Location.Origin != contracts.SourceLocationSDK || step.Callee.Location.SDKModulePath == nil || *step.Callee.Location.SDKModulePath != endpointSDKModule || step.Callee.Location.Path != "zscaler/zparequests.go" || step.Callee.Location.Function == nil || *step.Callee.Location.Function != "(*Client).NewRequestDo" || step.Callee.Location.Line != 16 || step.Callee.Location.Column != 23 {
		t.Errorf("endpoint authority chain %q terminal request callee = %+v, want (*Client).NewRequestDo at zscaler/zparequests.go:16:23", label, step.Callee)
	}
}

func bindingPaths(bindings []contracts.FileBinding) map[string]struct{} {
	result := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		result[binding.Path] = struct{}{}
	}
	return result
}

func bindingTreeDigest(bindings []contracts.FileBinding) string {
	ordered := append([]contracts.FileBinding(nil), bindings...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	hash := sha256.New()
	for _, binding := range ordered {
		_, _ = hash.Write([]byte(binding.Path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(binding.SHA256))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
