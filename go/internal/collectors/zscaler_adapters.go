package collectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// zscaler_adapters.go ports node-src/collectors/zscaler-adapters.ts: the
// four built-in Zscaler product adapters (zia/zpa/zcc/ztc), legacy vs
// OneAPI auth modes, token acquisition against the HttpTransport seam,
// fetch-debug diagnostics with FETCH_DEBUG masking semantics, and
// diagnostic-host derivation.

const oneAPIAudience = "https://api.zscaler.com"

// dnsLabel ports the DNS_LABEL regexp from
// node-src/collectors/zscaler-adapters.ts.
var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// truthyEnvValues ports the TRUTHY set from
// node-src/collectors/zscaler-adapters.ts, shared by
// CollectorAuthModeFromEnvironment (ZSCALER_USE_LEGACY_CLIENT) and
// debugVerbose (FETCH_DEBUG).
var truthyEnvValues = map[string]struct{}{"1": {}, "true": {}, "yes": {}, "on": {}}

// zpaLegacyBaseOrder is the literal key order of ZPA_LEGACY_BASES in
// node-src/collectors/zscaler-adapters.ts (minus its "" entry), preserved
// exactly for zpaLegacyBase's "known clouds" error text: JS's
// `Object.keys(ZPA_LEGACY_BASES)` walks a plain object's own string keys
// in insertion order, which is not alphabetical, so this cannot be
// rebuilt from zpaLegacyBases (a Go map, with no iteration order at all)
// via canonjson.SortedStrings without changing the rendered text.
var zpaLegacyBaseOrder = []string{"PRODUCTION", "ZPATWO", "BETA", "GOV", "GOVUS"}

// zpaLegacyBases ports ZPA_LEGACY_BASES from
// node-src/collectors/zscaler-adapters.ts.
var zpaLegacyBases = map[string]string{
	"":           "https://config.private.zscaler.com",
	"PRODUCTION": "https://config.private.zscaler.com",
	"ZPATWO":     "https://config.zpatwo.net",
	"BETA":       "https://config.zpabeta.net",
	"GOV":        "https://config.zpagov.net",
	"GOVUS":      "https://config.zpagov.us",
}

func requireEnvironment(environment Environment, name string) (string, error) {
	value, ok := environment[name]
	if !ok || value == "" {
		return "", fmt.Errorf("missing required env var %s", name)
	}
	return value, nil
}

// normalizedLabel ports normalizedLabel from
// node-src/collectors/zscaler-adapters.ts.
func normalizedLabel(value, label string, allowPlaceholder bool) (string, error) {
	text := strings.ToLower(strings.TrimSpace(value))
	if allowPlaceholder && text == "<vanity>" {
		return text, nil
	}
	if text == "" || !dnsLabel.MatchString(text) {
		return "", fmt.Errorf(
			"%s must be a DNS label (letters, digits, hyphen; no dots, slashes, or empty labels)", label,
		)
	}
	return text, nil
}

// normalizedCloud ports normalizedCloud from
// node-src/collectors/zscaler-adapters.ts.
func normalizedCloud(cloud, label string) (string, error) {
	text := strings.ToLower(strings.TrimSpace(cloud))
	if text == "" || text == "production" {
		return "", nil
	}
	return normalizedLabel(text, label, false)
}

// NormalizeLegacyBaseURL ports normalizeLegacyBaseUrl from
// node-src/collectors/zscaler-adapters.ts: validate a legacy
// private/custom host override without imposing an allowlist. name must
// be "ZIA_LEGACY_BASE_URL" or "ZPA_LEGACY_BASE_URL", matching the TS
// parameter's literal union type.
func NormalizeLegacyBaseURL(name, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || strings.ToLower(parsed.Scheme) != "https" || parsed.Hostname() == "" {
		return "", fmt.Errorf("%s must be an https:// host URL", name)
	}
	if parsed.User != nil {
		password, _ := parsed.User.Password()
		if parsed.User.Username() != "" || password != "" {
			return "", fmt.Errorf("%s must not contain username or password", name)
		}
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%s must not contain path, query, or fragment", name)
	}
	hostname := strings.ToLower(parsed.Hostname())
	for _, segment := range strings.Split(hostname, ".") {
		if _, err := normalizedLabel(segment, name+" host segment", false); err != nil {
			return "", err
		}
	}
	port := ""
	if parsed.Port() != "" {
		port = ":" + parsed.Port()
	}
	return "https://" + hostname + port, nil
}

func oneAPIGateway(cloud string) (string, error) {
	suffix, err := normalizedCloud(cloud, "ZSCALER_CLOUD")
	if err != nil {
		return "", err
	}
	if suffix == "" {
		return "https://api.zsapi.net", nil
	}
	return fmt.Sprintf("https://api.%s.zsapi.net", suffix), nil
}

func oneAPITokenHost(vanityDomain, cloud string, allowPlaceholder bool) (string, error) {
	vanity, err := normalizedLabel(vanityDomain, "ZSCALER_VANITY_DOMAIN", allowPlaceholder)
	if err != nil {
		return "", err
	}
	suffix, err := normalizedCloud(cloud, "ZSCALER_CLOUD")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://%s.zslogin%s.net", vanity, suffix), nil
}

func ziaLegacyBase(context CollectorContext) (string, error) {
	if context.ZiaLegacyBase != "" {
		return NormalizeLegacyBaseURL("ZIA_LEGACY_BASE_URL", context.ZiaLegacyBase)
	}
	if context.Cloud == "" {
		return "", errors.New(
			"ZIA_CLOUD is required in legacy mode (e.g. zscalertwo) — it selects the ZIA host https://zsapi.<cloud>.net",
		)
	}
	label, err := normalizedLabel(context.Cloud, "ZIA_CLOUD", false)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://zsapi.%s.net", label), nil
}

func zpaLegacyBaseOrUndefined(cloud string) (string, bool) {
	value, ok := zpaLegacyBases[strings.ToUpper(strings.TrimSpace(cloud))]
	return value, ok
}

func zpaLegacyBase(context CollectorContext) (string, error) {
	if context.ZpaLegacyBase != "" {
		return NormalizeLegacyBaseURL("ZPA_LEGACY_BASE_URL", context.ZpaLegacyBase)
	}
	base, ok := zpaLegacyBaseOrUndefined(context.ZpaCloud)
	if !ok {
		return "", fmt.Errorf(
			"unknown ZPA_CLOUD %s for the legacy config base — set ZPA_LEGACY_BASE_URL to the correct https://config.<cloud> host (known clouds: %s)",
			jsonQuote(context.ZpaCloud), strings.Join(zpaLegacyBaseOrder, ", "),
		)
	}
	return base, nil
}

func responseText(response HTTPResponse) (string, error) {
	if !utf8.Valid(response.Body) {
		return "", errors.New("authentication response is not valid UTF-8")
	}
	return string(response.Body), nil
}

// tokenField ports tokenField from node-src/collectors/zscaler-adapters.ts.
// A UTF-8 decode failure and a JSON parse failure collapse into the same
// "not JSON" message, matching the TS source: there,
// `JSON.parse(responseText(response))` runs the (possibly-throwing)
// responseText call inside the same try block as JSON.parse itself, so
// its throw is caught by the identical generic catch.
func tokenField(response HTTPResponse, key, label string) (string, error) {
	notJSON := fmt.Errorf(
		"%s: HTTP 200 but the response is not JSON (maintenance page? proxy interception?) — re-try, then check the auth endpoint with make fetch-diag",
		label,
	)
	text, err := responseText(response)
	if err != nil {
		return "", notJSON
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return "", notJSON
	}
	missing := fmt.Errorf(
		"%s: HTTP 200 but no %s in the response — check the API client's permissions/credentials for this product",
		label, jsonQuote(key),
	)
	obj, ok := parsed.(map[string]any)
	if !ok {
		return "", missing
	}
	value, has := obj[key]
	if !has {
		return "", missing
	}
	s, ok := value.(string)
	if !ok {
		return "", missing
	}
	return s, nil
}

func bearerContext(token string) CollectorAuthContext {
	return CollectorAuthContext{
		Headers: map[string]string{"Accept": "application/json", "Authorization": "Bearer " + token},
	}
}

// formURLEncodeComponent percent-encodes value per the WHATWG
// "application/x-www-form-urlencoded percent-encode set" -- the encoding
// `new URLSearchParams(...).toString()` performs in
// node-src/collectors/zscaler-adapters.ts's acquireOneApi/acquireZpaLegacy.
// This is deliberately a different unreserved set than percentEncode in
// rest.go (which ports rest.ts's own hand-written percentEncode for query
// strings and path expansion): URLSearchParams additionally leaves "*"
// literal and does *not* leave "~" literal, matching the URL Standard's
// form-urlencoded serializer rather than RFC 3986's unreserved set.
func formURLEncodeComponent(value string) string {
	var sb strings.Builder
	for i := 0; i < len(value); i++ {
		b := value[i]
		switch {
		case (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') ||
			b == '*' || b == '-' || b == '.' || b == '_':
			sb.WriteByte(b)
		case b == ' ':
			sb.WriteByte('+')
		default:
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String()
}

// formURLEncode joins an ordered list of key/value pairs the way
// `new URLSearchParams(pairs).toString()` does: '&'-joined
// "key=value" segments, in the caller-supplied order (URLSearchParams
// preserves array-literal insertion order, unlike Go's
// net/url.Values.Encode, which always sorts by key -- exactly the
// byte-parity hazard this package's queryPair type also exists to avoid
// in rest.go).
func formURLEncode(pairs [][2]string) string {
	parts := make([]string, len(pairs))
	for i, pair := range pairs {
		parts[i] = formURLEncodeComponent(pair[0]) + "=" + formURLEncodeComponent(pair[1])
	}
	return strings.Join(parts, "&")
}

func authPerformanceContext(input CollectorAcquireInput, endpointFamily string) *HTTPRequestPerformanceContext {
	if input.PerformanceContext == nil {
		return nil
	}
	return &HTTPRequestPerformanceContext{
		Classification: ClassificationAuthentication,
		EndpointFamily: endpointFamily,
		Phase:          input.PerformanceContext.Phase,
		Product:        input.PerformanceContext.Product,
		ResourceFamily: input.PerformanceContext.ResourceFamily,
	}
}

func acquireOneAPI(input CollectorAcquireInput) (CollectorAuthContext, error) {
	vanity, err := requireEnvironment(input.Environment, "ZSCALER_VANITY_DOMAIN")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	host, err := oneAPITokenHost(vanity, input.Environment["ZSCALER_CLOUD"], false)
	if err != nil {
		return CollectorAuthContext{}, err
	}
	tokenURL, err := url.Parse(host + "/oauth2/v1/token")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	clientID, err := requireEnvironment(input.Environment, "ZSCALER_CLIENT_ID")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	clientSecret, err := requireEnvironment(input.Environment, "ZSCALER_CLIENT_SECRET")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	body := formURLEncode([][2]string{
		{"grant_type", "client_credentials"},
		{"client_id", clientID},
		{"client_secret", clientSecret},
		{"audience", oneAPIAudience},
	})
	response, err := input.Transport.Request(HTTPRequest{
		Method:      "POST",
		URL:         tokenURL,
		Headers:     map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:        []byte(body),
		Performance: authPerformanceContext(input, "oauth2/v1/token"),
	})
	if err != nil {
		return CollectorAuthContext{}, err
	}
	if response.Status != 200 {
		return CollectorAuthContext{}, NewHTTPStatusError(
			fmt.Sprintf("OneAPI token request failed: HTTP %d", response.Status), response.Status,
		)
	}
	token, err := tokenField(response, "access_token", "OneAPI token")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	return bearerContext(token), nil
}

// sixDigits ports the regexp `/^[0-9]{6}$/` from obfuscateZiaApiKey in
// node-src/collectors/zscaler-adapters.ts.
var sixDigits = regexp.MustCompile(`^[0-9]{6}$`)

// ObfuscateZiaAPIKey ports obfuscateZiaApiKey from
// node-src/collectors/zscaler-adapters.ts: the legacy ZIA API-key
// obfuscation used by the public SDK. apiKey and timestamp are walked by
// Unicode code point ([]rune), matching the TS source's `[...value]`
// spread (code-point iteration), not by UTF-16 code unit or byte.
func ObfuscateZiaAPIKey(apiKey, timestamp string) (string, error) {
	keyRunes := []rune(apiKey)
	timestampRunes := []rune(timestamp)
	tooShort := errors.New("timestamp or api key below required length")
	if len(timestampRunes) < 6 || len(keyRunes) < 12 {
		return "", tooShort
	}
	high := string(timestampRunes[len(timestampRunes)-6:])
	if !sixDigits.MatchString(high) {
		return "", tooShort
	}
	parsedHigh, err := strconv.Atoi(high)
	if err != nil {
		return "", tooShort
	}
	low := fmt.Sprintf("%06d", parsedHigh>>1)
	var obfuscated strings.Builder
	for _, digit := range high {
		if index := int(digit - '0'); index >= 0 && index < len(keyRunes) {
			obfuscated.WriteRune(keyRunes[index])
		}
	}
	for _, digit := range low {
		if index := int(digit-'0') + 2; index >= 0 && index < len(keyRunes) {
			obfuscated.WriteRune(keyRunes[index])
		}
	}
	return obfuscated.String(), nil
}

func acquireZiaLegacy(input CollectorAcquireInput) (CollectorAuthContext, error) {
	// Ports `String(input.nowMs ?? Date.now())` from acquireZiaLegacy in
	// node-src/collectors/zscaler-adapters.ts.
	nowMs := time.Now().UnixMilli()
	if input.NowMs != nil {
		nowMs = *input.NowMs
	}
	timestamp := strconv.FormatInt(nowMs, 10)
	apiKey, err := requireEnvironment(input.Environment, "ZIA_API_KEY")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	obfuscated, err := ObfuscateZiaAPIKey(apiKey, timestamp)
	if err != nil {
		return CollectorAuthContext{}, err
	}
	username, err := requireEnvironment(input.Environment, "ZIA_USERNAME")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	password, err := requireEnvironment(input.Environment, "ZIA_PASSWORD")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	body, err := json.Marshal(ziaAuthBody{APIKey: obfuscated, Username: username, Password: password, Timestamp: timestamp})
	if err != nil {
		return CollectorAuthContext{}, err
	}
	base, err := ziaLegacyBase(input.Context)
	if err != nil {
		return CollectorAuthContext{}, err
	}
	requestURL, err := url.Parse(base + "/api/v1/authenticatedSession")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	response, err := input.Transport.Request(HTTPRequest{
		Method:      "POST",
		URL:         requestURL,
		Headers:     map[string]string{"Content-Type": "application/json"},
		Body:        body,
		Performance: authPerformanceContext(input, "api/v1/authenticatedSession"),
	})
	if err != nil {
		return CollectorAuthContext{}, err
	}
	if response.Status != 200 {
		return CollectorAuthContext{}, NewHTTPStatusError(
			fmt.Sprintf("ZIA session auth failed: HTTP %d", response.Status), response.Status,
		)
	}
	// The injected transport owns and persists the authenticated session cookie.
	return CollectorAuthContext{Headers: map[string]string{"Accept": "application/json"}}, nil
}

// ziaAuthBody ports the inline object literal
// `{ apiKey, username, password, timestamp }` from acquireZiaLegacy in
// node-src/collectors/zscaler-adapters.ts. Field declaration order matches
// the TS object literal's property order (json.Marshal on a struct emits
// fields in declaration order, unlike a Go map).
type ziaAuthBody struct {
	APIKey    string `json:"apiKey"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Timestamp string `json:"timestamp"`
}

func acquireZpaLegacy(input CollectorAcquireInput) (CollectorAuthContext, error) {
	clientID, err := requireEnvironment(input.Environment, "ZPA_CLIENT_ID")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	clientSecret, err := requireEnvironment(input.Environment, "ZPA_CLIENT_SECRET")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	base, err := zpaLegacyBase(input.Context)
	if err != nil {
		return CollectorAuthContext{}, err
	}
	requestURL, err := url.Parse(base + "/signin")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	body := formURLEncode([][2]string{{"client_id", clientID}, {"client_secret", clientSecret}})
	response, err := input.Transport.Request(HTTPRequest{
		Method:      "POST",
		URL:         requestURL,
		Headers:     map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:        []byte(body),
		Performance: authPerformanceContext(input, "signin"),
	})
	if err != nil {
		return CollectorAuthContext{}, err
	}
	if response.Status != 200 {
		return CollectorAuthContext{}, NewHTTPStatusError(
			fmt.Sprintf("ZPA signin failed: HTTP %d", response.Status), response.Status,
		)
	}
	token, err := tokenField(response, "access_token", "ZPA signin")
	if err != nil {
		return CollectorAuthContext{}, err
	}
	return bearerContext(token), nil
}

// zscalerProduct ports the literal union type
// `"zia" | "zpa" | "zcc" | "ztc"` that node-src/collectors/zscaler-adapters.ts's
// adapter()/composeProductUrl close over.
type zscalerProduct string

const (
	productZIA zscalerProduct = "zia"
	productZPA zscalerProduct = "zpa"
	productZCC zscalerProduct = "zcc"
	productZTC zscalerProduct = "ztc"
)

func composeProductURL(product zscalerProduct, input CollectorComposeUrlInput) (*url.URL, error) {
	if input.Mode == AuthModeOneAPI {
		gateway, err := oneAPIGateway(input.Context.Cloud)
		if err != nil {
			return nil, err
		}
		switch product {
		case productZIA:
			return url.Parse(gateway + "/zia/api/v1/" + input.Path)
		case productZPA:
			return url.Parse(gateway + "/zpa/mgmtconfig/v1/admin/customers/" + input.Context.CustomerID + "/" + input.Path)
		case productZCC:
			return url.Parse(gateway + "/" + input.Path)
		case productZTC:
			if strings.HasPrefix(input.Path, "/") {
				return url.Parse(gateway + input.Path)
			}
			return url.Parse(gateway + "/" + input.Path)
		default:
			return nil, fmt.Errorf("unknown zscaler product %q", product)
		}
	}
	switch product {
	case productZIA:
		base, err := ziaLegacyBase(input.Context)
		if err != nil {
			return nil, err
		}
		return url.Parse(base + "/api/v1/" + input.Path)
	case productZPA:
		base, err := zpaLegacyBase(input.Context)
		if err != nil {
			return nil, err
		}
		return url.Parse(base + "/mgmtconfig/v1/admin/customers/" + input.Context.CustomerID + "/" + input.Path)
	case productZCC:
		return nil, errors.New(`unknown auth_mode/product: 'legacy'/'zcc'`)
	default:
		return nil, errors.New(
			`ZTC legacy auth is not wired in the collector yet. Use OneAPI, or scope ZTC out of legacy runs with RESOURCE="zia zpa".`,
		)
	}
}

func newZscalerAdapter(product zscalerProduct) CollectorAdapter {
	return CollectorAdapter{
		Product: string(product),
		Acquire: func(input CollectorAcquireInput) (CollectorAuthContext, error) {
			if input.Mode == AuthModeOneAPI {
				return acquireOneAPI(input)
			}
			switch product {
			case productZIA:
				return acquireZiaLegacy(input)
			case productZPA:
				return acquireZpaLegacy(input)
			case productZCC:
				return CollectorAuthContext{}, errors.New(
					`ZCC has no legacy auth path — it is OneAPI-only. Use OneAPI, or scope ZCC out of legacy runs with RESOURCE="zia zpa".`,
				)
			default:
				return CollectorAuthContext{}, errors.New(
					`ZTC legacy auth is not wired in the collector yet. Use OneAPI, or scope ZTC out of legacy runs with RESOURCE="zia zpa".`,
				)
			}
		},
		ComposeURL: func(input CollectorComposeUrlInput) (*url.URL, error) {
			return composeProductURL(product, input)
		},
	}
}

// CreateZscalerCollectorAdapters ports createZscalerCollectorAdapters from
// node-src/collectors/zscaler-adapters.ts: built-in product adapters;
// resource selection remains registry-driven.
//
// The TS source returns a Map, whose `.keys()` iterates in insertion
// order ("zia", "zpa", "zcc", "ztc"); a Go map has no iteration order at
// all, and nothing in this package ever iterates this particular map to
// produce observable output (fetchResourcesBatch always walks
// FetchProducts's already-canonjson.SortedStrings-ordered product list,
// never this map directly), so that ordering is a Node-test-only
// artifact, not a behavioral contract -- see rest reviewer-attention
// notes.
func CreateZscalerCollectorAdapters() map[string]CollectorAdapter {
	return map[string]CollectorAdapter{
		"zia": newZscalerAdapter(productZIA),
		"zpa": newZscalerAdapter(productZPA),
		"zcc": newZscalerAdapter(productZCC),
		"ztc": newZscalerAdapter(productZTC),
	}
}

// ZscalerCollectorProviderSources ports ZSCALER_COLLECTOR_PROVIDER_SOURCES
// from node-src/collectors/zscaler-adapters.ts.
var ZscalerCollectorProviderSources = map[string]string{
	"zcc": "zscaler/zcc",
	"zia": "zscaler/zia",
	"zpa": "zscaler/zpa",
	"ztc": "zscaler/ztc",
}

// CreateZscalerCollectorAdaptersByProviderSource ports
// createZscalerCollectorAdaptersByProviderSource from
// node-src/collectors/zscaler-adapters.ts: closed built-in adapters keyed
// by the provider sources already declared by packs.
func CreateZscalerCollectorAdaptersByProviderSource() map[string]CollectorAdapter {
	byProduct := CreateZscalerCollectorAdapters()
	bySource := make(map[string]CollectorAdapter, len(ZscalerCollectorProviderSources))
	for product, providerSource := range ZscalerCollectorProviderSources {
		adapter, ok := byProduct[product]
		if !ok {
			// Unreachable: ZscalerCollectorProviderSources and
			// CreateZscalerCollectorAdapters both enumerate exactly
			// {zia, zpa, zcc, ztc}.
			panic(fmt.Sprintf("missing built-in collector adapter for product %s", product))
		}
		bySource[providerSource] = adapter
	}
	return bySource
}

// CollectorAuthModeFromEnvironment ports collectorAuthMode from
// node-src/collectors/zscaler-adapters.ts.
func CollectorAuthModeFromEnvironment(environment Environment) CollectorAuthMode {
	flag := strings.ToLower(strings.TrimSpace(environment["ZSCALER_USE_LEGACY_CLIENT"]))
	if _, truthy := truthyEnvValues[flag]; truthy {
		return AuthModeLegacy
	}
	return AuthModeOneAPI
}

// NewCollectorContextInput ports the options bag collectorContext accepts
// in node-src/collectors/zscaler-adapters.ts. Mode being "" means the TS
// `mode?: CollectorAuthMode` field was omitted (falls back to
// CollectorAuthModeFromEnvironment).
type NewCollectorContextInput struct {
	Environment    Environment
	NeededProducts map[string]struct{}
	Mode           CollectorAuthMode
}

// NewCollectorContext ports collectorContext from
// node-src/collectors/zscaler-adapters.ts.
func NewCollectorContext(input NewCollectorContextInput) (CollectorContext, error) {
	mode := input.Mode
	if mode == "" {
		mode = CollectorAuthModeFromEnvironment(input.Environment)
	}
	var customerID string
	if _, needed := input.NeededProducts["zpa"]; needed {
		id, err := requireEnvironment(input.Environment, "ZPA_CUSTOMER_ID")
		if err != nil {
			return CollectorContext{}, err
		}
		customerID = id
	} else {
		customerID = input.Environment["ZPA_CUSTOMER_ID"]
	}
	if mode == AuthModeOneAPI {
		return CollectorContext{Cloud: input.Environment["ZSCALER_CLOUD"], CustomerID: customerID}, nil
	}
	ziaBase, err := NormalizeLegacyBaseURL("ZIA_LEGACY_BASE_URL", input.Environment["ZIA_LEGACY_BASE_URL"])
	if err != nil {
		return CollectorContext{}, err
	}
	zpaBase, err := NormalizeLegacyBaseURL("ZPA_LEGACY_BASE_URL", input.Environment["ZPA_LEGACY_BASE_URL"])
	if err != nil {
		return CollectorContext{}, err
	}
	cloud := input.Environment["ZIA_CLOUD"]
	if cloud == "" {
		cloud = input.Environment["ZSCALER_CLOUD"]
	}
	return CollectorContext{
		Cloud:         cloud,
		CustomerID:    customerID,
		ZiaLegacyBase: ziaBase,
		ZpaCloud:      input.Environment["ZPA_CLOUD"],
		ZpaLegacyBase: zpaBase,
	}, nil
}

func debugVerbose(environment Environment) bool {
	flag := strings.ToLower(strings.TrimSpace(environment["FETCH_DEBUG"]))
	_, truthy := truthyEnvValues[flag]
	return truthy
}

func configuredHTTPSProxy(environment Environment) string {
	if value, ok := environment["https_proxy"]; ok {
		return value
	}
	return environment["HTTPS_PROXY"]
}

func safeLegacyBase(derive func() (string, error), override string) string {
	if override != "" {
		return override + " (override)"
	}
	value, err := derive()
	if err != nil {
		return fmt.Sprintf("<unresolved: %s>", err.Error())
	}
	return value
}

// FetchDebugLinesInput ports the options bag fetchDebugLines accepts in
// node-src/collectors/zscaler-adapters.ts.
type FetchDebugLinesInput struct {
	Environment Environment
	Context     CollectorContext
	Mode        CollectorAuthMode
	Products    map[string]struct{}
}

// FetchDebugLines ports fetchDebugLines from
// node-src/collectors/zscaler-adapters.ts.
func FetchDebugLines(input FetchDebugLinesInput) ([]string, error) {
	verbose := debugVerbose(input.Environment)
	masked := false
	identity := func(value string) string {
		if value != "" && !verbose {
			masked = true
			return "set"
		}
		if value == "" {
			return "<unset>"
		}
		return value
	}
	proxyState := "not set"
	if configuredHTTPSProxy(input.Environment) != "" {
		proxyState = "set"
	}
	lines := []string{
		fmt.Sprintf("fetch: auth mode = %s", input.Mode),
		fmt.Sprintf("fetch: proxy = %s", proxyState),
	}
	if input.Mode == AuthModeOneAPI {
		cloudDisplay := input.Environment["ZSCALER_CLOUD"]
		if cloudDisplay == "" {
			cloudDisplay = "(production)"
		}
		lines = append(lines,
			fmt.Sprintf("fetch: ZSCALER_CLOUD = %s", cloudDisplay),
			fmt.Sprintf("fetch: ZSCALER_VANITY_DOMAIN = %s", identity(input.Environment["ZSCALER_VANITY_DOMAIN"])),
		)
		if input.Context.CustomerID != "" {
			lines = append(lines, fmt.Sprintf("fetch: ZPA_CUSTOMER_ID = %s", identity(input.Context.CustomerID)))
		}
		configuredVanity := input.Environment["ZSCALER_VANITY_DOMAIN"]
		shownVanity := configuredVanity
		if shownVanity == "" {
			shownVanity = "<vanity>"
		}
		if !verbose {
			if configuredVanity != "" {
				masked = true
			}
			shownVanity = "<vanity>"
		}
		tokenHost, err := oneAPITokenHost(shownVanity, input.Environment["ZSCALER_CLOUD"], true)
		if err != nil {
			return nil, err
		}
		gateway, err := oneAPIGateway(input.Context.Cloud)
		if err != nil {
			return nil, err
		}
		lines = append(lines,
			fmt.Sprintf("fetch: token host = %s", tokenHost),
			fmt.Sprintf("fetch: gateway = %s", gateway),
		)
	} else {
		ziaCloudDisplay := input.Environment["ZIA_CLOUD"]
		if ziaCloudDisplay == "" {
			ziaCloudDisplay = "<unset>"
		}
		lines = append(lines, fmt.Sprintf("fetch: ZIA_CLOUD = %s", ziaCloudDisplay))
		if _, has := input.Products["zpa"]; has {
			zpaCloudDisplay := input.Environment["ZPA_CLOUD"]
			if zpaCloudDisplay == "" {
				zpaCloudDisplay = "(production)"
			}
			lines = append(lines, fmt.Sprintf("fetch: ZPA_CLOUD = %s", zpaCloudDisplay))
		}
		if input.Context.CustomerID != "" {
			lines = append(lines, fmt.Sprintf("fetch: ZPA_CUSTOMER_ID = %s", identity(input.Context.CustomerID)))
		}
		if _, has := input.Products["zia"]; has {
			lines = append(lines, fmt.Sprintf("fetch: zia base = %s", safeLegacyBase(
				func() (string, error) { return ziaLegacyBase(input.Context) }, input.Context.ZiaLegacyBase,
			)))
		}
		if _, has := input.Products["zpa"]; has {
			lines = append(lines, fmt.Sprintf("fetch: zpa base = %s", safeLegacyBase(
				func() (string, error) { return zpaLegacyBase(input.Context) }, input.Context.ZpaLegacyBase,
			)))
		}
	}
	if masked {
		lines = append(lines, "fetch: (vanity/customer-id hidden; set FETCH_DEBUG=1 to show)")
	}
	return lines, nil
}

// hostOf ports hostOf from node-src/collectors/zscaler-adapters.ts: the
// host[:port] portion of value, tolerating a bare host with no scheme.
func hostOf(value string) string {
	rest := value
	if idx := strings.Index(value, "//"); idx != -1 {
		rest = value[idx+2:]
	}
	if idx := strings.Index(rest, "/"); idx != -1 {
		rest = rest[:idx]
	}
	return rest
}

// DiagnosticHosts ports diagnosticHosts from
// node-src/collectors/zscaler-adapters.ts: unique HTTPS hosts contacted by
// the selected active Zscaler products.
func DiagnosticHosts(environment Environment, products map[string]struct{}) ([]string, error) {
	mode := CollectorAuthModeFromEnvironment(environment)
	if mode == AuthModeOneAPI {
		anyOneAPIProduct := false
		for _, product := range []string{"zcc", "zia", "zpa", "ztc"} {
			if _, has := products[product]; has {
				anyOneAPIProduct = true
				break
			}
		}
		if !anyOneAPIProduct {
			return []string{}, nil
		}
		vanity := environment["ZSCALER_VANITY_DOMAIN"]
		if vanity == "" {
			vanity = "<vanity>"
		}
		gateway, err := oneAPIGateway(environment["ZSCALER_CLOUD"])
		if err != nil {
			return nil, err
		}
		tokenHost, err := oneAPITokenHost(vanity, environment["ZSCALER_CLOUD"], true)
		if err != nil {
			return nil, err
		}
		hosts := []string{hostOf(gateway), hostOf(tokenHost)}
		return canonjson.SortedStrings(hosts), nil
	}
	hosts := make(map[string]struct{})
	ziaOverride, err := NormalizeLegacyBaseURL("ZIA_LEGACY_BASE_URL", environment["ZIA_LEGACY_BASE_URL"])
	if err != nil {
		return nil, err
	}
	zpaOverride, err := NormalizeLegacyBaseURL("ZPA_LEGACY_BASE_URL", environment["ZPA_LEGACY_BASE_URL"])
	if err != nil {
		return nil, err
	}
	if _, has := products["zia"]; has {
		cloud := environment["ZIA_CLOUD"]
		if cloud == "" {
			cloud = environment["ZSCALER_CLOUD"]
		}
		if cloud == "" {
			cloud = "<cloud>"
		}
		base := ziaOverride
		if base == "" {
			base = fmt.Sprintf("https://zsapi.%s.net", cloud)
		}
		hosts[hostOf(base)] = struct{}{}
	}
	if _, has := products["zpa"]; has {
		base := zpaOverride
		if base == "" {
			if fallback, ok := zpaLegacyBaseOrUndefined(environment["ZPA_CLOUD"]); ok {
				base = fallback
			} else {
				base = "https://config.<zpa-cloud>"
			}
		}
		hosts[hostOf(base)] = struct{}{}
	}
	names := make([]string, 0, len(hosts))
	for host := range hosts {
		names = append(names, host)
	}
	return canonjson.SortedStrings(names), nil
}
