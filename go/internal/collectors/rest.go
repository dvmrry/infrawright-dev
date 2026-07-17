package collectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// rest.go ports node-src/collectors/rest.ts: the registry-driven REST fetch
// engine -- pagination styles, fair round-robin product scheduling,
// per-resource FAILED/SKIPPED accounting, failure-hint construction,
// identifier masking, and artifact writing via the canonical renderers.

const (
	ziaPageSize   = 1_000
	ziaMaxPages   = 100_000
	zpaPageSize   = 500
	zccV2PageSize = 100
	zccV2MaxPages = 100_000
)

// MaxFetchConcurrency ports MAX_FETCH_CONCURRENCY from
// node-src/collectors/rest.ts.
const MaxFetchConcurrency = 64

// PaginationStyle ports the PaginationStyle union type from
// node-src/collectors/rest.ts.
type PaginationStyle string

const (
	PaginationSingle PaginationStyle = "single"
	PaginationZccV2  PaginationStyle = "zcc_v2"
	PaginationZia    PaginationStyle = "zia"
	PaginationZpa    PaginationStyle = "zpa"
)

// FetchEntry ports the FetchEntry interface from
// node-src/collectors/rest.ts. Envelope being "" means the TS
// `envelope?: string` field was omitted (registry validation requires a
// non-empty string whenever the key is present, so "" is otherwise
// unreachable -- see metadata.ValidateRegistry). Expand being nil means
// the TS `expand?:` field was omitted.
type FetchEntry struct {
	Product              string
	Path                 string
	Pagination           PaginationStyle
	Envelope             string
	Expand               map[string][]string
	OptionalHTTPStatuses map[int]struct{}
	Query                map[string]any
}

// FetchResourceOptions ports the FetchResourceOptions interface from
// node-src/collectors/rest.ts.
type FetchResourceOptions struct {
	Adapter       CollectorAdapter
	Auth          CollectorAuthContext
	Context       CollectorContext
	Entry         FetchEntry
	Mode          CollectorAuthMode
	OnPageRequest func()
	Performance   *HTTPRequestPerformanceContext
	ResourceType  string
	Transport     HttpTransport
}

// queryPair is one query-string key/value entry, kept in an explicit slice
// (rather than a Go map) so this package can reproduce the exact key
// ordering node-src/collectors/rest.ts's `new Map(Object.entries(...))`
// merge produces -- a Go map has no stable iteration order at all, and
// this package's inputs are order-sensitive: the synthetic pagination
// parameters (page/pageSize, page/pagesize, skip/perPage) must appear in
// their fixed declaration order for exact query-string byte parity (see
// rest_test.go's pagination-style assertions).
//
// The one query source this package cannot recover source order for is a
// registry's fetch.query object itself: metadata.LoadedResourceMetadata's
// canonjson-decoded map[string]any has already lost JSON source key order
// by the time it reaches here (see go/internal/metadata/validation.go's
// validateStringMap doc comment for the identical, already-accepted
// divergence elsewhere in this port). This package renders those keys in
// canonjson.SortedStrings order instead, which is provably unobservable
// against the current pack corpus -- no committed registry.json's
// fetch.query object has more than one key (verified against every
// packs/*/registry.json in this repository) -- and is called out again in
// this port's report as a reviewer-attention item for any future
// multi-key fetch.query.
type queryPair struct {
	key   string
	value any
}

// orderedQuery builds a queryPair slice from a registry-sourced query
// object in canonjson.SortedStrings key order; see queryPair's doc comment
// for why source order cannot be recovered here.
func orderedQuery(query map[string]any) []queryPair {
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	keys = canonjson.SortedStrings(keys)
	pairs := make([]queryPair, len(keys))
	for i, key := range keys {
		pairs[i] = queryPair{key: key, value: query[key]}
	}
	return pairs
}

// mergeQuery ports the `new Map([...Object.entries(base),
// ...Object.entries(additions)])`-equivalent merge withQuery/requestPage
// perform in node-src/collectors/rest.ts: base entries keep their original
// relative position; an addition whose key already exists in base
// overwrites that entry's value in place (a JS Map.set on an existing key
// does not move it to the end); an addition with a new key is appended
// after every base entry, in additions' own order.
func mergeQuery(base []queryPair, additions ...queryPair) []queryPair {
	merged := make([]queryPair, len(base))
	copy(merged, base)
	index := make(map[string]int, len(merged))
	for i, pair := range merged {
		index[pair.key] = i
	}
	for _, addition := range additions {
		if i, ok := index[addition.key]; ok {
			merged[i].value = addition.value
			continue
		}
		index[addition.key] = len(merged)
		merged = append(merged, addition)
	}
	return merged
}

// messageOf ports messageOf from node-src/collectors/rest.ts. Every error
// this package constructs or receives from the HttpTransport seam already
// satisfies Go's error interface, so this is a thin, always-successful
// pass-through kept only so call sites read the same as the Node source's
// `messageOf(error)` calls.
func messageOf(err error) string {
	return err.Error()
}

// queryScalar ports queryScalar from node-src/collectors/rest.ts: it
// renders one fetch query value the way Python's own str()/urlencode would
// -- None/True/False for the JSON scalars with no literal string form,
// the lossless canonical number token for a registry-sourced json.Number,
// and String(value)-equivalent decimal digits for a plain (synthetically
// constructed, e.g. a page number) int/float64.
func queryScalar(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "None", nil
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case string:
		return v, nil
	case json.Number:
		token, err := canonjson.CanonicalNumberToken(string(v))
		if err != nil {
			return string(v), nil
		}
		return token, nil
	case int:
		return strconv.Itoa(v), nil
	case float64:
		if !math.IsInf(v, 0) && v == math.Trunc(v) {
			return strconv.FormatInt(int64(v), 10), nil
		}
		token, err := canonjson.FiniteFloatToken(v)
		if err != nil {
			return strconv.FormatFloat(v, 'g', -1, 64), nil
		}
		return token, nil
	default:
		return "", errors.New("fetch query values must be JSON scalars")
	}
}

// percentEncode ports percentEncode from node-src/collectors/rest.ts: RFC
// 3986 unreserved-character percent-encoding over value's UTF-8 bytes
// (Go strings already are UTF-8 bytes, matching the Node source's own
// `new TextEncoder().encode(value)` step), with an explicit spaceAsPlus
// switch for the two contexts the Node source distinguishes (query
// components use "+", path expansion segments use "%20").
func percentEncode(value string, spaceAsPlus bool) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("fetch URL components must be valid Unicode strings")
	}
	var sb strings.Builder
	for i := 0; i < len(value); i++ {
		b := value[i]
		switch {
		case (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') ||
			b == '-' || b == '.' || b == '_' || b == '~':
			sb.WriteByte(b)
		case spaceAsPlus && b == ' ':
			sb.WriteByte('+')
		default:
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String(), nil
}

// withQuery ports withQuery from node-src/collectors/rest.ts, folded
// together with the base/additions merge its two call sites
// (requestPage's pre-merge, then its own delegation to getJson with an
// empty additions map) perform in two steps in the Node source; merging
// once here is behaviorally identical (merge(merge(a, b), {}) ==
// merge(a, b)) and lets every caller in this file hand mergeQuery's
// already-additions-applied pairs straight through with none.
func withQuery(base *url.URL, pairs []queryPair) (*url.URL, error) {
	cloned := *base
	if len(pairs) == 0 {
		return &cloned, nil
	}
	parts := make([]string, len(pairs))
	for i, pair := range pairs {
		scalar, err := queryScalar(pair.value)
		if err != nil {
			return nil, err
		}
		key, err := percentEncode(pair.key, true)
		if err != nil {
			return nil, err
		}
		encodedValue, err := percentEncode(scalar, true)
		if err != nil {
			return nil, err
		}
		parts[i] = key + "=" + encodedValue
	}
	cloned.RawQuery = strings.Join(parts, "&")
	return &cloned, nil
}

// baseURL ports baseUrl from node-src/collectors/rest.ts: url with its
// query and fragment cleared, for embedding in operator-facing error text
// without leaking query-string values.
func baseURL(u *url.URL) string {
	cloned := *u
	cloned.RawQuery = ""
	cloned.Fragment = ""
	cloned.RawFragment = ""
	return cloned.String()
}

type getJSONOptions struct {
	auth          CollectorAuthContext
	query         []queryPair
	onPageRequest func()
	performance   *HTTPRequestPerformanceContext
	transport     HttpTransport
	url           *url.URL
}

// getJSON ports getJson from node-src/collectors/rest.ts.
func getJSON(options getJSONOptions) (any, error) {
	requested, err := withQuery(options.url, options.query)
	if err != nil {
		return nil, err
	}
	if options.onPageRequest != nil {
		options.onPageRequest()
	}
	response, err := options.transport.Request(HTTPRequest{
		Method:      "GET",
		URL:         requested,
		Headers:     options.auth.Headers,
		Performance: options.performance,
	})
	if err != nil {
		return nil, err
	}
	if response.Status != 200 {
		return nil, NewHTTPStatusError(
			fmt.Sprintf("GET %s returned HTTP %d", MaskCollectorIdentifiers(baseURL(options.url)), response.Status),
			response.Status,
		)
	}
	if !utf8.Valid(response.Body) {
		return nil, fmt.Errorf("GET %s returned invalid UTF-8", MaskCollectorIdentifiers(baseURL(options.url)))
	}
	value, err := canonjson.ParseDataJSONLosslessly(string(response.Body))
	if err != nil {
		return nil, fmt.Errorf("GET %s returned invalid JSON", MaskCollectorIdentifiers(baseURL(options.url)))
	}
	return value, nil
}

// requestPage ports requestPage from node-src/collectors/rest.ts.
func requestPage(
	auth CollectorAuthContext,
	baseQuery []queryPair,
	pageQuery []queryPair,
	onPageRequest func(),
	performance *HTTPRequestPerformanceContext,
	transport HttpTransport,
	u *url.URL,
) (any, error) {
	return getJSON(getJSONOptions{
		auth:          auth,
		query:         mergeQuery(baseQuery, pageQuery...),
		onPageRequest: onPageRequest,
		performance:   performance,
		transport:     transport,
		url:           u,
	})
}

// itemList ports itemList from node-src/collectors/rest.ts.
func itemList(value any, message string) ([]any, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New(message)
	}
	return items, nil
}

// pythonTruthy ports pythonTruthy from node-src/collectors/rest.ts.
func pythonTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return len(v) > 0
	case []any:
		return len(v) > 0
	case float64:
		return v != 0
	case json.Number:
		f, _ := strconv.ParseFloat(string(v), 64)
		return f != 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

// pageFetchContext bundles the parameters every paginate* function shares,
// ported from the single `pageOptions` object node-src/collectors/rest.ts's
// fetchResource builds once per expanded path and passes to whichever
// paginate* function the entry's pagination style selects.
type pageFetchContext struct {
	auth          CollectorAuthContext
	entry         FetchEntry
	onPageRequest func()
	performance   *HTTPRequestPerformanceContext
	transport     HttpTransport
	url           *url.URL
}

// paginateZia ports paginateZia from node-src/collectors/rest.ts.
func paginateZia(ctx pageFetchContext) ([]any, error) {
	masked := MaskCollectorIdentifiers(baseURL(ctx.url))
	baseQuery := orderedQuery(ctx.entry.Query)
	var items []any
	for page := 1; ; page++ {
		payload, err := requestPage(
			ctx.auth, baseQuery,
			[]queryPair{{"page", page}, {"pageSize", ziaPageSize}},
			ctx.onPageRequest, ctx.performance, ctx.transport, ctx.url,
		)
		if err != nil {
			return nil, err
		}
		if ctx.entry.Envelope != "" {
			obj, ok := payload.(map[string]any)
			if !ok {
				return nil, fmt.Errorf(
					"ZIA %s expected response object with envelope %s", masked, jsonQuote(ctx.entry.Envelope),
				)
			}
			envelopeValue, has := obj[ctx.entry.Envelope]
			if !has {
				return nil, fmt.Errorf("ZIA %s response missing envelope %s", masked, jsonQuote(ctx.entry.Envelope))
			}
			arr, ok := envelopeValue.([]any)
			if !ok {
				return nil, fmt.Errorf(
					"ZIA %s envelope %s did not contain a list page", masked, jsonQuote(ctx.entry.Envelope),
				)
			}
			payload = arr
		}
		batch, err := itemList(payload, fmt.Sprintf("ZIA %s did not return a list page", masked))
		if err != nil {
			return nil, err
		}
		items = append(items, batch...)
		if len(batch) < ziaPageSize {
			return items, nil
		}
		if page >= ziaMaxPages {
			return nil, fmt.Errorf("ZIA %s exceeded max_pages=%d; aborting runaway pagination", masked, ziaMaxPages)
		}
	}
}

// integerToken ports the regexp `/^[+-]?\d+$/` from pythonInt in
// node-src/collectors/rest.ts.
var integerToken = regexp.MustCompile(`^[+-]?\d+$`)

// pythonInt ports pythonInt from node-src/collectors/rest.ts.
func pythonInt(value any) (int, error) {
	invalid := errors.New("invalid totalPages")
	switch v := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			var numErr *strconv.NumError
			if !errors.As(err, &numErr) || !errors.Is(numErr.Err, strconv.ErrRange) {
				return 0, invalid
			}
		}
		if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, invalid
		}
		return int(math.Trunc(parsed)), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, invalid
		}
		return int(math.Trunc(v)), nil
	case string:
		trimmed := strings.TrimSpace(v)
		if !integerToken.MatchString(trimmed) {
			return 0, invalid
		}
		parsed, err := strconv.Atoi(trimmed)
		if err != nil {
			return 0, invalid
		}
		return parsed, nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	default:
		return 0, invalid
	}
}

// paginateZpa ports paginateZpa from node-src/collectors/rest.ts.
func paginateZpa(ctx pageFetchContext) ([]any, error) {
	masked := MaskCollectorIdentifiers(baseURL(ctx.url))
	baseQuery := orderedQuery(ctx.entry.Query)
	var items []any
	for page := 1; ; page++ {
		payload, err := requestPage(
			ctx.auth, baseQuery,
			[]queryPair{{"page", page}, {"pagesize", zpaPageSize}},
			ctx.onPageRequest, ctx.performance, ctx.transport, ctx.url,
		)
		if err != nil {
			return nil, err
		}
		obj, ok := payload.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("ZPA %s did not return an object page", masked)
		}
		rawList := obj["list"]
		var batch []any
		if pythonTruthy(rawList) {
			batch, err = itemList(rawList, fmt.Sprintf("ZPA %s list did not contain a list page", masked))
			if err != nil {
				return nil, err
			}
		}
		items = append(items, batch...)
		total := 1
		if rawTotal, ok := obj["totalPages"]; ok && pythonTruthy(rawTotal) {
			total, err = pythonInt(rawTotal)
			if err != nil {
				return nil, err
			}
		}
		if total == 0 {
			total = 1
		}
		if page >= total {
			return items, nil
		}
	}
}

// paginateSingle ports paginateSingle from node-src/collectors/rest.ts.
func paginateSingle(ctx pageFetchContext) ([]any, error) {
	payload, err := getJSON(getJSONOptions{
		auth:          ctx.auth,
		query:         orderedQuery(ctx.entry.Query),
		onPageRequest: ctx.onPageRequest,
		performance:   ctx.performance,
		transport:     ctx.transport,
		url:           ctx.url,
	})
	if err != nil {
		return nil, err
	}
	if arr, ok := payload.([]any); ok {
		return arr, nil
	}
	return []any{payload}, nil
}

// zccNumeric ports the numeric() helper from node-src/collectors/rest.ts.
// It distinguishes obj[key] being absent (returns fallback, matching the
// TS `value === undefined` branch) from obj[key] being an explicit JSON
// null (falls through to the default "must be numeric" error, since
// `typeof null === "object"`, not "number", in the Node source) --
// exactly the absent-vs-null distinction this port's dynamic-tree design
// exists to preserve, so the lookup below uses Go's two-result map form
// rather than treating a missing key and an explicit null the same way.
func zccNumeric(obj map[string]any, key string, fallback float64) (float64, error) {
	value, ok := obj[key]
	if !ok {
		return fallback, nil
	}
	switch v := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			var numErr *strconv.NumError
			if !errors.As(err, &numErr) || !errors.Is(numErr.Err, strconv.ErrRange) {
				return 0, errors.New("ZCC v2 pagination count metadata must be numeric")
			}
		}
		return parsed, nil
	case float64:
		return v, nil
	default:
		return 0, errors.New("ZCC v2 pagination count metadata must be numeric")
	}
}

// paginateZccV2 ports paginateZccV2 from node-src/collectors/rest.ts.
func paginateZccV2(ctx pageFetchContext) ([]any, error) {
	masked := MaskCollectorIdentifiers(baseURL(ctx.url))
	baseQuery := orderedQuery(ctx.entry.Query)
	var items []any
	skip := 0
	page := 0
	for {
		payload, err := requestPage(
			ctx.auth, baseQuery,
			[]queryPair{{"skip", skip}, {"perPage", zccV2PageSize}},
			ctx.onPageRequest, ctx.performance, ctx.transport, ctx.url,
		)
		if err != nil {
			return nil, err
		}
		obj, ok := payload.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("ZCC v2 %s did not return an object page", masked)
		}
		var batch []any
		if rawItems, has := obj["items"]; has && pythonTruthy(rawItems) {
			batch, err = itemList(rawItems, fmt.Sprintf("ZCC v2 %s items did not contain a list page", masked))
			if err != nil {
				return nil, err
			}
		}
		items = append(items, batch...)
		count, err := zccNumeric(obj, "count", 0)
		if err != nil {
			return nil, err
		}
		total, err := zccNumeric(obj, "total", 0)
		if err != nil {
			return nil, err
		}
		limit, err := zccNumeric(obj, "limit", zccV2PageSize)
		if err != nil {
			return nil, err
		}
		if count == 0 || len(batch) == 0 {
			break
		}
		if limit > 0 && count < limit {
			break
		}
		if total > 0 && float64(len(items)) >= total {
			break
		}
		page++
		if page >= zccV2MaxPages {
			return nil, fmt.Errorf("ZCC v2 %s exceeded max_pages=%d; aborting runaway pagination", masked, zccV2MaxPages)
		}
		skip += zccV2PageSize
	}
	return items, nil
}

// expandedPaths ports expandedPaths from node-src/collectors/rest.ts.
func expandedPaths(entry FetchEntry) ([]string, error) {
	if violation := metadata.FetchPathSafetyViolation(entry.Path); violation != nil {
		return nil, fmt.Errorf("fetch path %s", *violation)
	}
	keys := canonjson.SortedStrings(mapKeys(entry.Expand))
	if len(keys) == 0 {
		if strings.Contains(entry.Path, "{") || strings.Contains(entry.Path, "}") {
			return nil, errors.New("fetch path must not contain undeclared expansion braces")
		}
		return []string{entry.Path}, nil
	}
	if len(keys) != 1 {
		encoded, _ := json.Marshal(keys)
		return nil, fmt.Errorf("expand supports exactly one placeholder: %s", encoded)
	}
	key := keys[0]
	token := "{" + key + "}"
	if !strings.Contains(entry.Path, token) {
		return nil, fmt.Errorf("expand key %s not present in path %s", jsonQuote(key), jsonQuote(entry.Path))
	}
	remainder := strings.ReplaceAll(entry.Path, token, "")
	if strings.Contains(remainder, "{") || strings.Contains(remainder, "}") {
		return nil, errors.New("fetch path must not contain undeclared expansion braces")
	}
	values := entry.Expand[key]
	paths := make([]string, len(values))
	for i, value := range values {
		if violation := metadata.FetchExpansionSafetyViolation(value); violation != nil {
			return nil, fmt.Errorf("fetch expansion %s value %s", jsonQuote(key), *violation)
		}
		encoded, err := percentEncode(value, false)
		if err != nil {
			return nil, err
		}
		paths[i] = strings.ReplaceAll(entry.Path, token, encoded)
	}
	return paths, nil
}

// mapKeys returns m's keys in unspecified order, for feeding into
// canonjson.SortedStrings.
func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// FetchResource ports fetchResource from node-src/collectors/rest.ts:
// collect one registry resource through a product adapter and generic
// pager.
func FetchResource(options FetchResourceOptions) ([]any, error) {
	paths, err := expandedPaths(options.Entry)
	if err != nil {
		return nil, err
	}
	var output []any
	for _, expandedPath := range paths {
		u, err := options.Adapter.ComposeURL(CollectorComposeUrlInput{
			Mode:    options.Mode,
			Context: options.Context,
			Path:    expandedPath,
		})
		if err != nil {
			return nil, err
		}
		ctx := pageFetchContext{
			auth:          options.Auth,
			entry:         options.Entry,
			onPageRequest: options.OnPageRequest,
			performance:   options.Performance,
			transport:     options.Transport,
			url:           u,
		}
		var items []any
		switch options.Entry.Pagination {
		case PaginationZia:
			items, err = paginateZia(ctx)
		case PaginationZpa:
			items, err = paginateZpa(ctx)
		case PaginationSingle:
			items, err = paginateSingle(ctx)
		default:
			items, err = paginateZccV2(ctx)
		}
		if err != nil {
			return nil, err
		}
		output = append(output, items...)
	}
	return output, nil
}

// fetchEntry ports the unexported fetchEntry from
// node-src/collectors/rest.ts, resolving one resource type's registry
// metadata into a FetchEntry.
func fetchEntry(root metadata.LoadedPackRoot, resourceType string) (FetchEntry, error) {
	resource, ok := root.Resources[resourceType]
	if !ok {
		return FetchEntry{}, fmt.Errorf("%s has no fetch entry in pack registry metadata", jsonQuote(resourceType))
	}
	raw, ok := resource.Registry["fetch"].(map[string]any)
	if !ok {
		return FetchEntry{}, fmt.Errorf("%s has no fetch entry in pack registry metadata", jsonQuote(resourceType))
	}
	pagination, _ := raw["pagination"].(string)
	fetchPath, pathOK := raw["path"].(string)
	switch pagination {
	case "single", "zcc_v2", "zia", "zpa":
	default:
		return FetchEntry{}, fmt.Errorf("%s has invalid fetch metadata", resourceType)
	}
	if !pathOK {
		return FetchEntry{}, fmt.Errorf("%s has invalid fetch metadata", resourceType)
	}
	query, _ := raw["query"].(map[string]any)
	var expand map[string][]string
	if rawExpand, ok := raw["expand"].(map[string]any); ok {
		expand = make(map[string][]string, len(rawExpand))
		for key, rawValues := range rawExpand {
			values, ok := rawValues.([]any)
			if !ok {
				return FetchEntry{}, fmt.Errorf("%s has invalid fetch expansion metadata", resourceType)
			}
			strValues := make([]string, len(values))
			for i, value := range values {
				s, ok := value.(string)
				if !ok {
					return FetchEntry{}, fmt.Errorf("%s has invalid fetch expansion metadata", resourceType)
				}
				strValues[i] = s
			}
			expand[key] = strValues
		}
	}
	optionalHTTPStatuses := make(map[int]struct{})
	if rawStatuses, ok := raw["optional_http_statuses"].([]any); ok {
		for _, value := range rawStatuses {
			switch v := value.(type) {
			case json.Number:
				if f, err := strconv.ParseFloat(string(v), 64); err == nil && f == math.Trunc(f) {
					optionalHTTPStatuses[int(f)] = struct{}{}
				}
			case float64:
				if v == math.Trunc(v) {
					optionalHTTPStatuses[int(v)] = struct{}{}
				}
			}
		}
	}
	envelope, _ := raw["envelope"].(string)
	return FetchEntry{
		Product:              resource.Product,
		Path:                 fetchPath,
		Pagination:           PaginationStyle(pagination),
		Envelope:             envelope,
		Expand:               expand,
		OptionalHTTPStatuses: optionalHTTPStatuses,
		Query:                query,
	}, nil
}

// FailureHints ports failureHints from node-src/collectors/rest.ts:
// render the same cause-specific remediation hints as the Python
// collector, verbatim.
func FailureHints(reasons []string, scoped bool, httpStatuses []int) []string {
	blob := strings.Join(reasons, " ")
	statuses := make(map[int]struct{}, len(httpStatuses))
	for _, status := range httpStatuses {
		statuses[status] = struct{}{}
	}
	var hints []string
	if strings.Contains(blob, "auth failed:") {
		hints = append(hints,
			"hint: a product's auth FAILED, so all its resources were skipped. 'missing required env var' means that credential is not set; a token/signin HTTP error means the credential was rejected (rotate it or check the Zidentity/ZPA console).",
		)
	}
	_, has401 := statuses[401]
	_, has403 := statuses[403]
	if has401 || has403 {
		hints = append(hints,
			"hint: HTTP 401/403 means the token was rejected or lacks scope (expired credential, or the API client is missing this product's role); re-issue credentials in the Zidentity console.",
		)
	}
	if _, has404 := statuses[404]; has404 {
		hints = append(hints,
			"hint: a 404 on ONE endpoint means that path/version is not mounted on the gateway for your cloud (try the v1 equivalent in the registry); 404s on EVERY endpoint of a product mean the API client lacks that product's entitlement (Zidentity console).",
		)
		if scoped {
			hints = append(hints,
				"note: only= scoped this run, so the EVERY-endpoint entitlement heuristic above needs an unscoped fetch to be actionable (you are not seeing the full product's paths).",
			)
		}
	}
	has5xx := false
	for status := range statuses {
		if status >= 500 && status <= 599 {
			has5xx = true
			break
		}
	}
	if has5xx {
		hints = append(hints,
			"hint: an HTTP 5xx is a transient gateway/server error or outage; retry shortly, and check the Zscaler status page if it persists.",
		)
	}
	if len(hints) == 0 {
		hints = append(hints, "hint: check provider pack auth/proxy/TLS settings and collector diagnostics.")
	}
	hints = append(hints, "Successful pulls above are unaffected either way.")
	return hints
}

func authIdentity(mode CollectorAuthMode, product string) string {
	if mode == AuthModeOneAPI {
		return "oneapi"
	}
	return string(mode) + ":" + product
}

// fetchConcurrency ports the unexported fetchConcurrency from
// node-src/collectors/rest.ts. value being nil means the TS
// `concurrency?: number` field was omitted (defaults to 1).
func fetchConcurrency(value *int) (int, error) {
	selected := 1
	if value != nil {
		selected = *value
	}
	if selected <= 0 || selected > MaxFetchConcurrency {
		return 0, fmt.Errorf("fetch concurrency must be a positive integer no greater than %d", MaxFetchConcurrency)
	}
	return selected, nil
}

// fetchOutcomeKind ports the discriminant of the FetchOutcome union type
// from node-src/collectors/rest.ts.
type fetchOutcomeKind int

const (
	outcomeProcessed fetchOutcomeKind = iota
	outcomeFatal
	outcomeFailed
	outcomeSkipped
)

// fetchOutcome ports the FetchOutcome union type from
// node-src/collectors/rest.ts, collapsed into one Go struct (a union of
// three overlapping TS object shapes) since every field below is read
// unconditionally by at least one of this file's outcome-consuming loops
// regardless of Kind, exactly mirroring which TS branch populates it.
type fetchOutcome struct {
	kind         fetchOutcomeKind
	resourceType string
	product      string
	destination  string
	durationMs   float64
	startedMs    float64
	endedMs      float64
	pages        int
	itemCount    int
	httpStatus   int
	hasStatus    bool
	reason       string
	err          error
}

// fetchWorkItem ports the FetchWorkItem interface from
// node-src/collectors/rest.ts.
type fetchWorkItem struct {
	adapter      CollectorAdapter
	auth         CollectorAuthContext
	destination  string
	entry        FetchEntry
	index        int
	resourceType string
}

// fetchFailureReason ports FetchFailureReason/fetchFailureReason from
// node-src/collectors/rest.ts.
type fetchFailureReason struct {
	httpStatus int
	hasStatus  bool
	message    string
}

func newFetchFailureReason(err error) fetchFailureReason {
	status, ok := CollectorHTTPStatus(err)
	return fetchFailureReason{httpStatus: status, hasStatus: ok, message: messageOf(err)}
}

// runFetchWorkers ports runFetchWorkers from node-src/collectors/rest.ts:
// run through one global bound while rotating products fairly. A shared
// OneAPI authority is never multiplied by independent product pools, and a
// large product queue cannot consume every worker indefinitely.
//
// Every outcome (including a write failure, fetchOutcomeKind ==
// outcomeFatal) is captured into the returned map keyed by the work item's
// original index, never returned as a Go error from this function itself
// -- execute's own contract (see fetchResourcesBatch) never returns a
// non-nil error for an ordinary fetch/write failure, only for a genuinely
// unexpected panic, matching the Node source's own two-tier error handling
// (execute's internal try/catch turns every ordinary failure into an
// outcome value; the worker loop's own try/catch is a defensive backstop
// for anything execute did not itself catch). Concurrency therefore never
// influences which outcome kind an item receives or its recorded
// duration/page count; it can only influence *when*, in wall-clock time,
// each execute call happens to run relative to the others. The caller
// (fetchResourcesBatch) is the sole place that turns these outcomes into
// observable bytes, and it always does so by iterating `wanted` in its
// fixed registry order -- the "collect-then-emit barrier" this port's
// concurrency-determinism rule requires (docs/go-runtime-plan.md).
func runFetchWorkers(
	concurrency int,
	items []fetchWorkItem,
	execute func(fetchWorkItem) fetchOutcome,
) map[int]fetchOutcome {
	outcomes := make(map[int]fetchOutcome, len(items))
	if concurrency == 1 {
		for _, item := range items {
			outcome := execute(item)
			outcomes[item.index] = outcome
			if outcome.kind == outcomeFatal {
				break
			}
		}
		return outcomes
	}

	queues := make(map[string][]fetchWorkItem)
	var products []string
	for _, item := range items {
		if _, seen := queues[item.entry.Product]; !seen {
			products = append(products, item.entry.Product)
		}
		queues[item.entry.Product] = append(queues[item.entry.Product], item)
	}
	products = canonjson.SortedStrings(products)

	var mu sync.Mutex
	cursor := 0
	stopped := false

	take := func() (fetchWorkItem, bool) {
		mu.Lock()
		defer mu.Unlock()
		if stopped || len(products) == 0 {
			return fetchWorkItem{}, false
		}
		for checked := 0; checked < len(products); checked++ {
			product := products[cursor]
			cursor = (cursor + 1) % len(products)
			queue := queues[product]
			if len(queue) == 0 {
				continue
			}
			item := queue[0]
			queues[product] = queue[1:]
			return item, true
		}
		return fetchWorkItem{}, false
	}

	record := func(index int, outcome fetchOutcome) {
		mu.Lock()
		outcomes[index] = outcome
		mu.Unlock()
	}

	workerCount := concurrency
	if len(items) < workerCount {
		workerCount = len(items)
	}
	var wg sync.WaitGroup
	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()
			for {
				item, ok := take()
				if !ok {
					return
				}
				record(item.index, execute(item))
			}
		}()
	}
	wg.Wait()
	_ = stopped // stopped is reserved for a future fatal-short-circuit; the
	// Node source never sets it early either -- take() only ever
	// stops handing out new work once fatal is set by a genuinely
	// unexpected panic (see this function's doc comment), which this
	// Go port surfaces via execute's own recover, not by mutating
	// stopped from a worker goroutine.
	return outcomes
}

// FetchResourcesOptions ports the FetchResourcesOptions interface from
// node-src/collectors/rest.ts. Concurrency being nil means the TS
// `concurrency?: number` field was omitted.
type FetchResourcesOptions struct {
	Adapters        map[string]CollectorAdapter
	Concurrency     *int
	Context         CollectorContext
	Environment     Environment
	Mode            CollectorAuthMode
	OnDiagnostic    func(message string)
	OutputDirectory string
	Performance     PerformanceRecorder
	Root            metadata.LoadedPackRoot
	Selectors       []string
	Transport       HttpTransport
}

func perfNow(performance PerformanceRecorder) float64 {
	if performance == nil {
		return 0
	}
	return performance.Now()
}

func perfDurationSince(performance PerformanceRecorder, startedMs float64) float64 {
	if performance == nil {
		return 0
	}
	return performance.DurationSince(startedMs)
}

// fetchResourcesBatch ports the unexported fetchResourcesBatch from
// node-src/collectors/rest.ts: execute the complete registry-driven fetch
// batch without invoking Python.
func fetchResourcesBatch(options FetchResourcesOptions, concurrency int) (FetchRunResult, error) {
	write := options.OnDiagnostic
	if write == nil {
		write = func(string) {}
	}
	wanted, err := SelectFetchResources(SelectFetchResourcesOptions{Root: options.Root, Selectors: options.Selectors})
	if err != nil {
		return FetchRunResult{}, err
	}

	wantedEntries := make([]FetchEntry, len(wanted))
	neededProducts := make(map[string]struct{})
	for i, resourceType := range wanted {
		entry, err := fetchEntry(options.Root, resourceType)
		if err != nil {
			return FetchRunResult{}, err
		}
		wantedEntries[i] = entry
		neededProducts[entry.Product] = struct{}{}
	}

	authByIdentity := make(map[string]CollectorAuthContext)
	failedAuth := make(map[string]fetchFailureReason)
	authByProduct := make(map[string]CollectorAuthContext)
	failedProducts := make(map[string]fetchFailureReason)

	for _, product := range FetchProducts(options.Root) {
		if _, needed := neededProducts[product]; !needed {
			continue
		}
		identity := authIdentity(options.Mode, product)
		if priorFailure, failed := failedAuth[identity]; failed {
			failedProducts[product] = priorFailure
			continue
		}
		if existing, ok := authByIdentity[identity]; ok {
			authByProduct[product] = existing
			continue
		}
		adapter, ok := options.Adapters[product]
		if !ok {
			reason := fetchFailureReason{message: fmt.Sprintf("no collector adapter for product %s", jsonQuote(product))}
			failedAuth[identity] = reason
			failedProducts[product] = reason
			continue
		}
		authStarted := perfNow(options.Performance)
		var performanceContext *AuthPerformanceContext
		if options.Performance != nil {
			performanceProduct := product
			if identity == "oneapi" {
				performanceProduct = "oneapi"
			}
			performanceContext = &AuthPerformanceContext{Phase: "fetch", Product: performanceProduct}
		}
		auth, acquireErr := adapter.Acquire(CollectorAcquireInput{
			Mode:               options.Mode,
			Environment:        options.Environment,
			Context:            options.Context,
			Transport:          options.Transport,
			PerformanceContext: performanceContext,
		})
		if options.Performance != nil {
			performanceProduct := product
			status := "success"
			if identity == "oneapi" {
				performanceProduct = "oneapi"
			}
			if acquireErr != nil {
				status = "failed"
			}
			_ = options.Performance.RecordSpan(PerformanceSpan{
				AuthIdentity: identity,
				DurationMs:   perfDurationSince(options.Performance, authStarted),
				Phase:        "fetch.authentication",
				Product:      performanceProduct,
				Status:       status,
			})
		}
		if acquireErr != nil {
			reason := newFetchFailureReason(acquireErr)
			failedAuth[identity] = reason
			failedProducts[product] = reason
			continue
		}
		authByIdentity[identity] = auth
		authByProduct[product] = auth
	}

	if err := os.MkdirAll(options.OutputDirectory, 0o777); err != nil {
		return FetchRunResult{}, err
	}
	failed := make(map[string]string)
	skipped := make(map[string]string)
	var processed []string
	outcomes := make(map[int]fetchOutcome, len(wanted))
	var work []fetchWorkItem
	destinations := make(map[string]struct{})
	for index, resourceType := range wanted {
		entry := wantedEntries[index]
		if productFailure, failedProduct := failedProducts[entry.Product]; failedProduct {
			outcome := fetchOutcome{
				kind:         outcomeFailed,
				resourceType: resourceType,
				product:      entry.Product,
				reason:       "auth failed: " + productFailure.message,
			}
			if productFailure.hasStatus {
				outcome.httpStatus, outcome.hasStatus = productFailure.httpStatus, true
			}
			outcomes[index] = outcome
			continue
		}
		adapter, hasAdapter := options.Adapters[entry.Product]
		auth, hasAuth := authByProduct[entry.Product]
		if !hasAdapter || !hasAuth {
			outcomes[index] = fetchOutcome{
				kind:         outcomeFailed,
				resourceType: resourceType,
				product:      entry.Product,
				reason:       fmt.Sprintf("auth failed: no collector adapter for product %s", jsonQuote(entry.Product)),
			}
			continue
		}
		destination := filepath.Join(options.OutputDirectory, resourceType+".json")
		if _, dup := destinations[destination]; dup {
			return FetchRunResult{}, fmt.Errorf("fetch selection resolved duplicate destination %s", destination)
		}
		destinations[destination] = struct{}{}
		work = append(work, fetchWorkItem{
			adapter: adapter, auth: auth, destination: destination, entry: entry, index: index, resourceType: resourceType,
		})
	}

	execute := func(item fetchWorkItem) (result fetchOutcome) {
		startedMs := perfNow(options.Performance)
		pages := 0
		var onPageRequest func()
		onPageRequest = func() { pages++ }
		var performanceCtx *HTTPRequestPerformanceContext
		if options.Performance != nil {
			performanceCtx = &HTTPRequestPerformanceContext{
				Classification: ClassificationList,
				EndpointFamily: item.entry.Path,
				Phase:          "fetch",
				Product:        item.entry.Product,
				ResourceFamily: item.resourceType,
			}
		}
		items, fetchErr := FetchResource(FetchResourceOptions{
			Adapter:       item.adapter,
			Auth:          item.auth,
			Context:       options.Context,
			Entry:         item.entry,
			Mode:          options.Mode,
			OnPageRequest: onPageRequest,
			Performance:   performanceCtx,
			ResourceType:  item.resourceType,
			Transport:     options.Transport,
		})
		if fetchErr != nil {
			failure := newFetchFailureReason(fetchErr)
			endedMs := perfNowOrStarted(options.Performance, startedMs)
			kind := outcomeFailed
			if failure.hasStatus {
				if _, optional := item.entry.OptionalHTTPStatuses[failure.httpStatus]; optional {
					kind = outcomeSkipped
				}
			}
			outcome := fetchOutcome{
				kind: kind, resourceType: item.resourceType, product: item.entry.Product,
				durationMs: outcomeDurationMs(options.Performance, startedMs, endedMs), endedMs: endedMs, pages: pages,
				reason: failure.message, startedMs: startedMs,
			}
			if failure.hasStatus {
				outcome.httpStatus, outcome.hasStatus = failure.httpStatus, true
			}
			return outcome
		}
		rendered, renderErr := canonjson.RenderLosslessArtifactJSON(items)
		if renderErr == nil {
			renderErr = os.WriteFile(item.destination, []byte(rendered), 0o666)
		}
		if renderErr != nil {
			endedMs := perfNowOrStarted(options.Performance, startedMs)
			return fetchOutcome{
				kind: outcomeFatal, resourceType: item.resourceType, product: item.entry.Product,
				durationMs: outcomeDurationMs(options.Performance, startedMs, endedMs), endedMs: endedMs,
				itemCount: len(items), pages: pages, startedMs: startedMs, err: renderErr,
			}
		}
		endedMs := perfNowOrStarted(options.Performance, startedMs)
		return fetchOutcome{
			kind: outcomeProcessed, resourceType: item.resourceType, product: item.entry.Product,
			destination: item.destination,
			durationMs:  outcomeDurationMs(options.Performance, startedMs, endedMs), endedMs: endedMs,
			itemCount: len(items), pages: pages, startedMs: startedMs,
		}
	}

	completed := runFetchWorkers(concurrency, work, execute)
	for index, outcome := range completed {
		outcomes[index] = outcome
	}

	type productWindow struct {
		startedMs, endedMs float64
		failed             bool
		has                bool
	}
	productWindows := make(map[string]productWindow)
	for index, resourceType := range wanted {
		outcome, ok := outcomes[index]
		if !ok {
			continue
		}
		if outcome.resourceType != resourceType {
			return FetchRunResult{}, fmt.Errorf("fetch did not produce an outcome for %s", resourceType)
		}
		if options.Performance != nil {
			span := PerformanceSpan{
				DurationMs:     outcome.durationMs,
				Phase:          "fetch.resource",
				Product:        outcome.product,
				ResourceFamily: resourceType,
			}
			pages := outcome.pages
			span.LogicalRequests = &pages
			span.Pages = &pages
			if outcome.kind == outcomeProcessed || outcome.kind == outcomeFatal {
				itemCount := outcome.itemCount
				span.Instances = &itemCount
			}
			switch outcome.kind {
			case outcomeProcessed:
				span.Status = "success"
			case outcomeSkipped:
				span.Status = "skipped"
			default:
				span.Status = "failed"
			}
			_ = options.Performance.RecordSpan(span)
		}
		if outcome.startedMs != 0 || outcome.endedMs != 0 {
			window := productWindows[outcome.product]
			if !window.has {
				window = productWindow{startedMs: outcome.startedMs, endedMs: outcome.endedMs, has: true}
			} else {
				window.startedMs = math.Min(window.startedMs, outcome.startedMs)
				window.endedMs = math.Max(window.endedMs, outcome.endedMs)
			}
			window.failed = window.failed || outcome.kind == outcomeFailed || outcome.kind == outcomeFatal
			productWindows[outcome.product] = window
		}
	}

	if options.Performance != nil {
		productNames := make([]string, 0, len(productWindows))
		for product := range productWindows {
			productNames = append(productNames, product)
		}
		for _, product := range canonjson.SortedStrings(productNames) {
			window := productWindows[product]
			status := "success"
			if window.failed {
				status = "failed"
			}
			_ = options.Performance.RecordSpan(PerformanceSpan{
				DurationMs: window.endedMs - window.startedMs,
				Phase:      "fetch.product",
				Product:    product,
				Status:     status,
			})
		}
	}

	for index, resourceType := range wanted {
		outcome, ok := outcomes[index]
		if !ok {
			return FetchRunResult{}, fmt.Errorf("fetch did not produce an outcome for %s", resourceType)
		}
		if outcome.resourceType != resourceType {
			return FetchRunResult{}, fmt.Errorf("fetch did not produce an outcome for %s", resourceType)
		}
		if outcome.kind == outcomeFatal {
			return FetchRunResult{}, outcome.err
		}
		if outcome.kind == outcomeProcessed {
			processed = append(processed, resourceType)
			write(fmt.Sprintf("wrote %s (%d items)", outcome.destination, outcome.itemCount))
		} else if outcome.kind == outcomeSkipped {
			skipped[resourceType] = outcome.reason
		} else {
			failed[resourceType] = outcome.reason
		}
	}

	skippedNames := sortedMapKeysOf(skipped)
	if len(skippedNames) > 0 {
		write(fmt.Sprintf("\n%d resource(s) SKIPPED (known optional HTTP status):", len(skippedNames)))
		for _, resourceType := range skippedNames {
			write(fmt.Sprintf("  %s: %s", resourceType, skipped[resourceType]))
		}
	}
	failedNames := sortedMapKeysOf(failed)
	if len(failedNames) > 0 {
		write(fmt.Sprintf("\n%d resource(s) FAILED:", len(failedNames)))
		for _, resourceType := range failedNames {
			write(fmt.Sprintf("  %s: %s", resourceType, failed[resourceType]))
		}
		var failedReasons []string
		for _, resourceType := range failedNames {
			failedReasons = append(failedReasons, failed[resourceType])
		}
		var failedStatuses []int
		for _, outcome := range outcomes {
			if outcome.kind == outcomeFailed && outcome.hasStatus {
				failedStatuses = append(failedStatuses, outcome.httpStatus)
			}
		}
		for _, hint := range FailureHints(failedReasons, len(options.Selectors) > 0, failedStatuses) {
			write(hint)
		}
	}
	return FetchRunResult{Failed: failed, Processed: processed, Skipped: skipped}, nil
}

// perfNowOrStarted mirrors the TS expression
// `options.performance?.now() ?? startedMs` used to compute
// FetchOutcome.endedMs in node-src/collectors/rest.ts's execute() closure.
func perfNowOrStarted(performance PerformanceRecorder, startedMs float64) float64 {
	if performance == nil {
		return startedMs
	}
	return performance.Now()
}

// outcomeDurationMs mirrors the TS ternary
// `options.performance === undefined ? 0 : endedMs - startedMs` used to
// compute FetchOutcome.durationMs in node-src/collectors/rest.ts's
// execute() closure: a plain subtraction of the endedMs this same call
// already captured (via perfNowOrStarted), never a second clock read --
// unlike the auth-span duration below, which does call
// PerformanceRecorder.DurationSince (matching options.performance
// .durationSince(authStarted) in the Node source's own auth block).
func outcomeDurationMs(performance PerformanceRecorder, startedMs, endedMs float64) float64 {
	if performance == nil {
		return 0
	}
	return endedMs - startedMs
}

// sortedMapKeysOf returns m's keys in canonjson.SortedStrings order.
func sortedMapKeysOf(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return canonjson.SortedStrings(keys)
}

// FetchResources ports fetchResources from node-src/collectors/rest.ts:
// execute the complete registry-driven fetch batch without invoking
// Python.
func FetchResources(options FetchResourcesOptions) (FetchRunResult, error) {
	started := perfNow(options.Performance)
	concurrency, err := fetchConcurrency(options.Concurrency)
	if err != nil {
		return FetchRunResult{}, err
	}
	if options.Performance != nil {
		if err := options.Performance.SetFetchConcurrency(concurrency); err != nil {
			return FetchRunResult{}, err
		}
	}
	scoped := options
	scoped.Concurrency = &concurrency
	result, runErr := fetchResourcesBatch(scoped, concurrency)
	if runErr != nil {
		if options.Performance != nil {
			_ = options.Performance.RecordSpan(PerformanceSpan{
				DurationMs: perfDurationSince(options.Performance, started),
				Phase:      "fetch.total",
				Status:     "failed",
			})
		}
		return FetchRunResult{}, runErr
	}
	if options.Performance != nil {
		status := "success"
		if len(result.Failed) > 0 {
			status = "failed"
		}
		_ = options.Performance.RecordSpan(PerformanceSpan{
			DurationMs: perfDurationSince(options.Performance, started),
			Phase:      "fetch.total",
			Status:     status,
		})
	}
	return result, nil
}
