package collectors

// zscaler_adapters_test.go ports
// the original test corpus.

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
)

func emptyContext() CollectorContext { return CollectorContext{} }

type recordingTransport struct {
	requests  []HTTPRequest
	responses []HTTPResponse
}

func (r *recordingTransport) Request(request HTTPRequest) (HTTPResponse, error) {
	r.requests = append(r.requests, request)
	if len(r.responses) == 0 {
		return HTTPResponse{}, errors.New("test transport ran out of responses")
	}
	next := r.responses[0]
	r.responses = r.responses[1:]
	return next, nil
}

func (r *recordingTransport) Close() error { return nil }

func newRecordingTransport(t *testing.T, responses ...HTTPResponse) *recordingTransport {
	t.Helper()
	return &recordingTransport{responses: responses}
}

func TestCollectorAuthModeMatchesPythonTruthyVocabulary(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", " yes ", "on"} {
		if got := CollectorAuthModeFromEnvironment(Environment{"ZSCALER_USE_LEGACY_CLIENT": value}); got != AuthModeLegacy {
			t.Errorf("CollectorAuthModeFromEnvironment(%q) = %v, want legacy", value, got)
		}
	}
	for _, value := range []string{"", "0", "false", "disabled"} {
		if got := CollectorAuthModeFromEnvironment(Environment{"ZSCALER_USE_LEGACY_CLIENT": value}); got != AuthModeOneAPI {
			t.Errorf("CollectorAuthModeFromEnvironment(%q) = %v, want oneapi", value, got)
		}
	}
	if got := CollectorAuthModeFromEnvironment(Environment{}); got != AuthModeOneAPI {
		t.Errorf("CollectorAuthModeFromEnvironment(unset) = %v, want oneapi", got)
	}
}

func TestCollectorContextScopesCustomerIDAndIgnoresStaleZiaCloudInOneAPI(t *testing.T) {
	got, err := NewCollectorContext(NewCollectorContextInput{
		Environment: Environment{
			"ZIA_CLOUD":       "stale-zia",
			"ZPA_CUSTOMER_ID": "customer",
			"ZSCALER_CLOUD":   "production",
		},
		Mode:           AuthModeOneAPI,
		NeededProducts: map[string]struct{}{"zia": {}},
	})
	if err != nil {
		t.Fatalf("NewCollectorContext: %v", err)
	}
	want := CollectorContext{Cloud: "production", CustomerID: "customer"}
	if got != want {
		t.Errorf("NewCollectorContext = %+v, want %+v", got, want)
	}

	_, err = NewCollectorContext(NewCollectorContextInput{
		Environment:    Environment{},
		NeededProducts: map[string]struct{}{"zpa": {}},
	})
	if err == nil || !strings.Contains(err.Error(), "missing required env var ZPA_CUSTOMER_ID") {
		t.Errorf("expected a missing ZPA_CUSTOMER_ID error, got %v", err)
	}
}

func TestLegacyContextValidatesHostOverridesAndRetainsZpaCloud(t *testing.T) {
	got, err := NewCollectorContext(NewCollectorContextInput{
		Environment: Environment{
			"ZIA_CLOUD":           "zscalertwo",
			"ZIA_LEGACY_BASE_URL": "https://ZIA.example.test/",
			"ZPA_CLOUD":           "ZPATWO",
			"ZPA_CUSTOMER_ID":     "customer",
			"ZPA_LEGACY_BASE_URL": "https://ZPA.example.test:8443",
		},
		Mode:           AuthModeLegacy,
		NeededProducts: map[string]struct{}{"zia": {}, "zpa": {}},
	})
	if err != nil {
		t.Fatalf("NewCollectorContext: %v", err)
	}
	want := CollectorContext{
		Cloud:         "zscalertwo",
		CustomerID:    "customer",
		ZiaLegacyBase: "https://zia.example.test",
		ZpaCloud:      "ZPATWO",
		ZpaLegacyBase: "https://zpa.example.test:8443",
	}
	if got != want {
		t.Errorf("NewCollectorContext = %+v, want %+v", got, want)
	}

	for _, value := range []string{
		"http://example.test",
		"https://user:secret@example.test",
		"https://example.test/path",
		"https://example.test?query=1",
		"https://example.test#fragment",
	} {
		if _, err := NormalizeLegacyBaseURL("ZIA_LEGACY_BASE_URL", value); err == nil || !strings.Contains(err.Error(), "ZIA_LEGACY_BASE_URL") {
			t.Errorf("NormalizeLegacyBaseURL(%q) error = %v, want a ZIA_LEGACY_BASE_URL error", value, err)
		}
	}
}

func TestAllProductsShareOneAPIAuthAndComposeTheirOwnURLs(t *testing.T) {
	adapters := CreateZscalerCollectorAdapters()
	wantProducts := map[string]struct{}{"zia": {}, "zpa": {}, "zcc": {}, "ztc": {}}
	if len(adapters) != len(wantProducts) {
		t.Fatalf("len(adapters) = %d, want %d", len(adapters), len(wantProducts))
	}
	for product := range wantProducts {
		if _, ok := adapters[product]; !ok {
			t.Errorf("adapters missing product %s", product)
		}
	}
	for product, adapter := range adapters {
		transport := newRecordingTransport(t, jsonResponse(t, map[string]any{"access_token": "token"}, 200))
		auth, err := adapter.Acquire(CollectorAcquireInput{
			Context: CollectorContext{Cloud: "production", CustomerID: "123"},
			Environment: Environment{
				"ZSCALER_CLIENT_ID":     "client",
				"ZSCALER_CLIENT_SECRET": "secret",
				"ZSCALER_CLOUD":         "production",
				"ZSCALER_VANITY_DOMAIN": "tenant",
			},
			Mode:      AuthModeOneAPI,
			Transport: transport,
		})
		if err != nil {
			t.Fatalf("%s acquire: %v", product, err)
		}
		wantHeaders := map[string]string{"Accept": "application/json", "Authorization": "Bearer token"}
		if !mapsEqual(auth.Headers, wantHeaders) {
			t.Errorf("%s auth headers = %v, want %v", product, auth.Headers, wantHeaders)
		}
		if got := transport.requests[0].URL.String(); got != "https://tenant.zslogin.net/oauth2/v1/token" {
			t.Errorf("%s token URL = %s, want https://tenant.zslogin.net/oauth2/v1/token", product, got)
		}
		wantBody := "grant_type=client_credentials&client_id=client&client_secret=secret&audience=https%3A%2F%2Fapi.zscaler.com"
		if got := string(transport.requests[0].Body); got != wantBody {
			t.Errorf("%s token body = %s, want %s", product, got, wantBody)
		}
	}

	context := CollectorContext{Cloud: "zscalertwo", CustomerID: "customer-7"}
	cases := []struct {
		product string
		path    string
		want    string
	}{
		{"zia", "urlCategories", "https://api.zscalertwo.zsapi.net/zia/api/v1/urlCategories"},
		{"zpa", "segmentGroup", "https://api.zscalertwo.zsapi.net/zpa/mgmtconfig/v1/admin/customers/customer-7/segmentGroup"},
		{"zcc", "zcc/papi/public/v1/test", "https://api.zscalertwo.zsapi.net/zcc/papi/public/v1/test"},
		{"ztc", "/ztw/api/v1/test", "https://api.zscalertwo.zsapi.net/ztw/api/v1/test"},
	}
	for _, tc := range cases {
		u, err := adapters[tc.product].ComposeURL(CollectorComposeUrlInput{Mode: AuthModeOneAPI, Context: context, Path: tc.path})
		if err != nil {
			t.Fatalf("%s ComposeURL: %v", tc.product, err)
		}
		if got := u.String(); got != tc.want {
			t.Errorf("%s ComposeURL = %s, want %s", tc.product, got, tc.want)
		}
	}
}

func TestAuthenticationFailuresRetainHTTPStatus(t *testing.T) {
	zia := CreateZscalerCollectorAdapters()["zia"]
	_, err := zia.Acquire(CollectorAcquireInput{
		Context: emptyContext(),
		Environment: Environment{
			"ZSCALER_CLIENT_ID":     "client",
			"ZSCALER_CLIENT_SECRET": "secret",
			"ZSCALER_VANITY_DOMAIN": "tenant",
		},
		Mode:      AuthModeOneAPI,
		Transport: newRecordingTransport(t, jsonResponse(t, map[string]any{}, 403)),
	})
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected *HTTPStatusError, got %v", err)
	}
	if statusErr.Status != 403 {
		t.Errorf("statusErr.Status = %d, want 403", statusErr.Status)
	}
	if statusErr.Error() != "OneAPI token request failed: HTTP 403" {
		t.Errorf("statusErr.Error() = %q, want %q", statusErr.Error(), "OneAPI token request failed: HTTP 403")
	}
}

func TestOneAPIHostInputsRejectCloudAndVanitySmuggling(t *testing.T) {
	adapter := CreateZscalerCollectorAdapters()["zia"]
	_, err := adapter.Acquire(CollectorAcquireInput{
		Context: emptyContext(),
		Environment: Environment{
			"ZSCALER_CLIENT_ID":     "client",
			"ZSCALER_CLIENT_SECRET": "secret",
			"ZSCALER_CLOUD":         ".attacker.test/x",
			"ZSCALER_VANITY_DOMAIN": "tenant",
		},
		Mode:      AuthModeOneAPI,
		Transport: newRecordingTransport(t),
	})
	if err == nil || !strings.Contains(err.Error(), "ZSCALER_CLOUD must be a DNS label") {
		t.Errorf("expected a ZSCALER_CLOUD DNS-label error, got %v", err)
	}

	_, err = adapter.Acquire(CollectorAcquireInput{
		Context: emptyContext(),
		Environment: Environment{
			"ZSCALER_CLIENT_ID":     "client",
			"ZSCALER_CLIENT_SECRET": "secret",
			"ZSCALER_VANITY_DOMAIN": "tenant.attacker",
		},
		Mode:      AuthModeOneAPI,
		Transport: newRecordingTransport(t),
	})
	if err == nil || !strings.Contains(err.Error(), "ZSCALER_VANITY_DOMAIN must be a DNS label") {
		t.Errorf("expected a ZSCALER_VANITY_DOMAIN DNS-label error, got %v", err)
	}
}

func TestZiaLegacyAuthObfuscatesKeyAndUsesTransportCookieSession(t *testing.T) {
	obfuscated, err := ObfuscateZiaAPIKey("0123456789ab", "1700001234567")
	if err != nil {
		t.Fatalf("ObfuscateZiaAPIKey: %v", err)
	}
	if obfuscated != "2345673394a5" {
		t.Errorf("ObfuscateZiaAPIKey = %s, want 2345673394a5", obfuscated)
	}

	transport := newRecordingTransport(t, jsonResponse(t, map[string]any{}, 200))
	nowMs := int64(1_700_001_234_567)
	adapter := CreateZscalerCollectorAdapters()["zia"]
	auth, err := adapter.Acquire(CollectorAcquireInput{
		Context: CollectorContext{Cloud: "zscalertwo"},
		Environment: Environment{
			"ZIA_API_KEY":  "0123456789ab",
			"ZIA_PASSWORD": "password",
			"ZIA_USERNAME": "user",
		},
		Mode:      AuthModeLegacy,
		NowMs:     &nowMs,
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !mapsEqual(auth.Headers, map[string]string{"Accept": "application/json"}) {
		t.Errorf("auth headers = %v", auth.Headers)
	}
	if got := transport.requests[0].URL.String(); got != "https://zsapi.zscalertwo.net/api/v1/authenticatedSession" {
		t.Errorf("request URL = %s", got)
	}
	var body map[string]any
	if err := json.Unmarshal(transport.requests[0].Body, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	want := map[string]any{"apiKey": "2345673394a5", "password": "password", "timestamp": "1700001234567", "username": "user"}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("request body[%s] = %v, want %v", key, body[key], value)
		}
	}

	u, err := adapter.ComposeURL(CollectorComposeUrlInput{
		Context: CollectorContext{Cloud: "zscalertwo"}, Mode: AuthModeLegacy, Path: "urlCategories",
	})
	if err != nil {
		t.Fatalf("ComposeURL: %v", err)
	}
	if got := u.String(); got != "https://zsapi.zscalertwo.net/api/v1/urlCategories" {
		t.Errorf("ComposeURL = %s", got)
	}
}

func TestZpaLegacyAuthAndCloudBasesMatchProviderHosts(t *testing.T) {
	adapter := CreateZscalerCollectorAdapters()["zpa"]
	cases := []struct {
		cloud string
		want  string
	}{
		{"", "https://config.private.zscaler.com"},
		{"production", "https://config.private.zscaler.com"},
		{"zpatwo", "https://config.zpatwo.net"},
		{"beta", "https://config.zpabeta.net"},
		{"gov", "https://config.zpagov.net"},
		{"govus", "https://config.zpagov.us"},
	}
	for _, tc := range cases {
		u, err := adapter.ComposeURL(CollectorComposeUrlInput{
			Context: CollectorContext{CustomerID: "customer", ZpaCloud: tc.cloud}, Mode: AuthModeLegacy, Path: "segmentGroup",
		})
		if err != nil {
			t.Fatalf("ComposeURL(%q): %v", tc.cloud, err)
		}
		if got := u.Scheme + "://" + u.Host; got != tc.want {
			t.Errorf("ComposeURL(%q) origin = %s, want %s", tc.cloud, got, tc.want)
		}
	}
	_, err := adapter.ComposeURL(CollectorComposeUrlInput{
		Context: CollectorContext{CustomerID: "customer", ZpaCloud: "private"}, Mode: AuthModeLegacy, Path: "segmentGroup",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown ZPA_CLOUD") {
		t.Errorf("expected an unknown ZPA_CLOUD error, got %v", err)
	}

	transport := newRecordingTransport(t, jsonResponse(t, map[string]any{"access_token": "zpa-token"}, 200))
	auth, err := adapter.Acquire(CollectorAcquireInput{
		Context:     CollectorContext{CustomerID: "customer", ZpaCloud: "ZPATWO"},
		Environment: Environment{"ZPA_CLIENT_ID": "client", "ZPA_CLIENT_SECRET": "secret"},
		Mode:        AuthModeLegacy,
		Transport:   transport,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got := transport.requests[0].URL.String(); got != "https://config.zpatwo.net/signin" {
		t.Errorf("request URL = %s", got)
	}
	if got := string(transport.requests[0].Body); got != "client_id=client&client_secret=secret" {
		t.Errorf("request body = %s", got)
	}
	if !mapsEqual(auth.Headers, map[string]string{"Accept": "application/json", "Authorization": "Bearer zpa-token"}) {
		t.Errorf("auth headers = %v", auth.Headers)
	}
}

func TestLegacyZccAndZtcFailuresRetainProductScopingRemediation(t *testing.T) {
	adapters := CreateZscalerCollectorAdapters()
	_, err := adapters["zcc"].Acquire(CollectorAcquireInput{
		Context: emptyContext(), Environment: Environment{}, Mode: AuthModeLegacy, Transport: newRecordingTransport(t),
	})
	if err == nil || !regexp.MustCompile(`ZCC has no legacy auth path.*RESOURCE="zia zpa"`).MatchString(err.Error()) {
		t.Errorf("expected a ZCC legacy-auth-path error, got %v", err)
	}
	_, err = adapters["ztc"].Acquire(CollectorAcquireInput{
		Context: emptyContext(), Environment: Environment{}, Mode: AuthModeLegacy, Transport: newRecordingTransport(t),
	})
	if err == nil || !regexp.MustCompile(`ZTC legacy auth is not wired.*RESOURCE="zia zpa"`).MatchString(err.Error()) {
		t.Errorf("expected a ZTC legacy-auth-path error, got %v", err)
	}
}

func TestDiagnosticHelpersMaskIdentitiesAndDeriveSameHosts(t *testing.T) {
	got := MaskCollectorIdentifiers("https://tenant.zsloginzscalertwo.net/zpa/customers/123/segmentGroup?customer=visible")
	want := "https://<vanity>.zsloginzscalertwo.net/zpa/customers/<customer-id>/segmentGroup?customer=visible"
	if got != want {
		t.Errorf("MaskCollectorIdentifiers = %s, want %s", got, want)
	}

	environment := Environment{
		"HTTPS_PROXY":           "https://secret-proxy.example",
		"ZPA_CUSTOMER_ID":       "customer",
		"ZSCALER_CLOUD":         "zscalertwo",
		"ZSCALER_VANITY_DOMAIN": "tenant",
	}
	context, err := NewCollectorContext(NewCollectorContextInput{
		Environment: environment, Mode: AuthModeOneAPI, NeededProducts: map[string]struct{}{"zpa": {}},
	})
	if err != nil {
		t.Fatalf("NewCollectorContext: %v", err)
	}
	lines, err := FetchDebugLines(FetchDebugLinesInput{
		Context: context, Environment: environment, Mode: AuthModeOneAPI, Products: map[string]struct{}{"zpa": {}},
	})
	if err != nil {
		t.Fatalf("FetchDebugLines: %v", err)
	}
	want2 := []string{
		"fetch: auth mode = oneapi",
		"fetch: proxy = set",
		"fetch: ZSCALER_CLOUD = zscalertwo",
		"fetch: ZSCALER_VANITY_DOMAIN = set",
		"fetch: ZPA_CUSTOMER_ID = set",
		"fetch: token host = https://<vanity>.zsloginzscalertwo.net",
		"fetch: gateway = https://api.zscalertwo.zsapi.net",
		"fetch: (vanity/customer-id hidden; set FETCH_DEBUG=1 to show)",
	}
	if !equalStrings(lines, want2) {
		t.Errorf("FetchDebugLines = %v, want %v", lines, want2)
	}

	lines2, err := FetchDebugLines(FetchDebugLinesInput{
		Context: emptyContext(), Environment: Environment{"HTTPS_PROXY": "https://upper.example", "https_proxy": ""},
		Mode: AuthModeOneAPI, Products: map[string]struct{}{"zia": {}},
	})
	if err != nil {
		t.Fatalf("FetchDebugLines: %v", err)
	}
	if lines2[1] != "fetch: proxy = not set" {
		t.Errorf("lines2[1] = %s, want fetch: proxy = not set", lines2[1])
	}

	lines3, err := FetchDebugLines(FetchDebugLinesInput{
		Context: emptyContext(), Environment: Environment{"HTTP_PROXY": "http://http-only.example"},
		Mode: AuthModeOneAPI, Products: map[string]struct{}{"zia": {}},
	})
	if err != nil {
		t.Fatalf("FetchDebugLines: %v", err)
	}
	if lines3[1] != "fetch: proxy = not set" {
		t.Errorf("lines3[1] = %s, want fetch: proxy = not set", lines3[1])
	}

	hosts, err := DiagnosticHosts(environment, map[string]struct{}{"zia": {}, "zpa": {}})
	if err != nil {
		t.Fatalf("DiagnosticHosts: %v", err)
	}
	wantHosts := []string{"api.zscalertwo.zsapi.net", "tenant.zsloginzscalertwo.net"}
	if !equalStrings(hosts, wantHosts) {
		t.Errorf("DiagnosticHosts = %v, want %v", hosts, wantHosts)
	}

	hosts2, err := DiagnosticHosts(Environment{
		"ZIA_CLOUD": "zscalertwo", "ZPA_CLOUD": "GOVUS", "ZSCALER_USE_LEGACY_CLIENT": "1",
	}, map[string]struct{}{"zia": {}, "zpa": {}, "zcc": {}})
	if err != nil {
		t.Fatalf("DiagnosticHosts: %v", err)
	}
	wantHosts2 := []string{"config.zpagov.us", "zsapi.zscalertwo.net"}
	if !equalStrings(hosts2, wantHosts2) {
		t.Errorf("DiagnosticHosts (legacy) = %v, want %v", hosts2, wantHosts2)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
