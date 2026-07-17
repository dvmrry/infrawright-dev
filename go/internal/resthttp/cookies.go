package resthttp

//go:generate go run generate_publicsuffix.go

import (
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	publicSuffixICANN   = uint8(1)
	publicSuffixPrivate = uint8(2)
	publicSuffixAll     = publicSuffixICANN | publicSuffixPrivate
)

type publicSuffixTrie struct {
	labelUnits   []uint16
	edgeOffset   []uint32
	edgeHash     []uint32
	wildcardEdge []int32
}

func buildPublicSuffixTrie() publicSuffixTrie {
	if len(publicSuffixEdgeStart) != len(publicSuffixNodeFlags)+1 ||
		len(publicSuffixEdgeLength) != len(publicSuffixEdgeChild) ||
		int(publicSuffixEdgeStart[len(publicSuffixEdgeStart)-1]) != len(publicSuffixEdgeLength) {
		panic("resthttp: malformed pinned public-suffix trie shape")
	}
	trie := publicSuffixTrie{
		labelUnits:   utf16.Encode([]rune(publicSuffixLabelText)),
		edgeOffset:   make([]uint32, len(publicSuffixEdgeLength)),
		edgeHash:     make([]uint32, len(publicSuffixEdgeLength)),
		wildcardEdge: make([]int32, len(publicSuffixNodeFlags)),
	}
	for index := range trie.wildcardEdge {
		trie.wildcardEdge[index] = -1
	}
	offset := 0
	for node := range publicSuffixNodeFlags {
		for edge := int(publicSuffixEdgeStart[node]); edge < int(publicSuffixEdgeStart[node+1]); edge++ {
			length := int(publicSuffixEdgeLength[edge])
			end := offset + length
			if end > len(trie.labelUnits) || int(publicSuffixEdgeChild[edge]) >= len(publicSuffixNodeFlags) {
				panic("resthttp: malformed pinned public-suffix trie edge")
			}
			trie.edgeOffset[edge] = uint32(offset)
			hash := uint32(5381)
			for index := end - 1; index >= offset; index-- {
				hash = hash*33 ^ uint32(trie.labelUnits[index])
			}
			trie.edgeHash[edge] = hash
			if length == 1 && trie.labelUnits[offset] == '*' {
				trie.wildcardEdge[node] = int32(edge)
			}
			offset = end
		}
	}
	if offset != len(trie.labelUnits) {
		panic("resthttp: malformed pinned public-suffix trie labels")
	}
	return trie
}

var pinnedPublicSuffixTrie = buildPublicSuffixTrie()

func (t *publicSuffixTrie) labelEquals(edge int, hostname []uint16, start, length int) bool {
	if int(publicSuffixEdgeLength[edge]) != length {
		return false
	}
	offset := int(t.edgeOffset[edge])
	for index := 0; index < length; index++ {
		if t.labelUnits[offset+index] != hostname[start+index] {
			return false
		}
	}
	return true
}

func (t *publicSuffixTrie) findEdge(node int, hash uint32, hostname []uint16, start, length int) int {
	low := int(publicSuffixEdgeStart[node])
	high := int(publicSuffixEdgeStart[node+1])
	for low < high {
		middle := (low + high) / 2
		value := t.edgeHash[middle]
		switch {
		case value < hash:
			low = middle + 1
		case value > hash:
			high = middle
		default:
			for edge := middle; edge >= low && t.edgeHash[edge] == hash; edge-- {
				if t.labelEquals(edge, hostname, start, length) {
					return edge
				}
			}
			for edge := middle + 1; edge < high && t.edgeHash[edge] == hash; edge++ {
				if t.labelEquals(edge, hostname, start, length) {
					return edge
				}
			}
			return -1
		}
	}
	return -1
}

type publicSuffixMatch struct {
	node  int
	start int
	end   int
	found bool
}

func (t *publicSuffixTrie) walk(hostname []uint16, root int, allowed uint8) publicSuffixMatch {
	node := root
	end := len(hostname)
	hash := uint32(5381)
	match := publicSuffixMatch{node: -1}
	for index := len(hostname) - 1; index >= 0; index-- {
		code := hostname[index]
		if code == '.' {
			start := index + 1
			edge := t.findEdge(node, hash, hostname, start, end-start)
			if edge == -1 {
				edge = int(t.wildcardEdge[node])
			}
			if edge == -1 {
				return match
			}
			node = int(publicSuffixEdgeChild[edge])
			if publicSuffixNodeFlags[node]&allowed != 0 {
				match = publicSuffixMatch{node: node, start: start, end: end, found: true}
			}
			end = index
			hash = 5381
		} else {
			hash = hash*33 ^ uint32(code)
		}
	}

	edge := t.findEdge(node, hash, hostname, 0, end)
	if edge == -1 {
		edge = int(t.wildcardEdge[node])
	}
	if edge != -1 {
		node = int(publicSuffixEdgeChild[edge])
		if publicSuffixNodeFlags[node]&allowed != 0 {
			match = publicSuffixMatch{node: node, start: 0, end: end, found: true}
		}
	}
	return match
}

func utf16String(input []uint16) string {
	return string(utf16.Decode(input))
}

func (t *publicSuffixTrie) publicSuffix(domain string) string {
	hostname := utf16.Encode([]rune(strings.ToLower(domain)))
	if exception := t.walk(hostname, publicSuffixExceptionsRoot, publicSuffixAll); exception.found {
		return utf16String(hostname[exception.end+1:])
	}
	if rule := t.walk(hostname, publicSuffixRulesRoot, publicSuffixAll); rule.found {
		return utf16String(hostname[rule.start:])
	}
	for index := len(hostname) - 1; index >= 0; index-- {
		if hostname[index] == '.' {
			return utf16String(hostname[index+1:])
		}
	}
	return utf16String(hostname)
}

// collectorPublicSuffixList is an exact Go port of the flat ICANN+private
// suffix trie shipped by the repository-pinned tldts 7.4.8. Tough-cookie uses
// that trie with both rule classes enabled. The generated data's provenance
// and required MIT notice live beside this file.
type collectorPublicSuffixList struct{}

func (collectorPublicSuffixList) PublicSuffix(domain string) string {
	if _, err := netip.ParseAddr(domain); err == nil {
		return domain
	}
	return pinnedPublicSuffixTrie.publicSuffix(domain)
}

func (collectorPublicSuffixList) String() string {
	return "tldts 7.4.8 ICANN+private public suffix list"
}

type toughCookieStorage interface {
	SetCookies(*url.URL, []*http.Cookie)
	Cookies(*url.URL) []*http.Cookie
}

// toughCookieJar preserves tough-cookie's host canonicalization and
// synchronous response-update ordering while delegating expiry, path matching,
// and creation ordering to net/http's jar.
type toughCookieJar struct {
	mu                  sync.Mutex
	regular             toughCookieStorage
	trailingSingleLabel toughCookieStorage
}

var _ http.CookieJar = (*toughCookieJar)(nil)

func newToughCookieJar() (*toughCookieJar, error) {
	options := &cookiejar.Options{PublicSuffixList: collectorPublicSuffixList{}}
	regular, err := cookiejar.New(options)
	if err != nil {
		return nil, err
	}
	trailingSingleLabel, err := cookiejar.New(options)
	if err != nil {
		return nil, err
	}
	return &toughCookieJar{
		regular:             regular,
		trailingSingleLabel: trailingSingleLabel,
	}, nil
}

func isSingleLabelTrailingDotHost(host string) bool {
	return strings.HasSuffix(host, ".") &&
		!strings.Contains(strings.TrimSuffix(host, "."), ".")
}

func (j *toughCookieJar) storage(target *url.URL) (toughCookieStorage, *url.URL) {
	if target == nil {
		return nil, nil
	}
	host := target.Hostname()
	if strings.HasSuffix(host, ".") {
		if !isSingleLabelTrailingDotHost(host) {
			// MemoryCookieStore asks permuteDomain for multi-label lookups.
			// tough-cookie 6.0.2 removes the terminal dot while generating
			// those permutations, so it never finds the literal stored domain.
			return nil, nil
		}
		// Go removes the terminal dot. A separate jar retains tough-cookie's
		// exact-host boundary while preserving same-dot single-label lookups.
		return j.trailingSingleLabel, target
	}
	address, err := netip.ParseAddr(host)
	if err != nil || !address.Is6() {
		return j.regular, target
	}
	context := *target
	// Go's jar compares an IPv6 Domain attribute with URL.Host, including
	// brackets. tough-cookie canonicalizes both to the unbracketed address.
	context.Host = address.String()
	return j.regular, &context
}

func normalizedToughCookieJarCookies(target *url.URL, cookies []*http.Cookie) []*http.Cookie {
	normalized := make([]*http.Cookie, 0, len(cookies))
	rejectDomainAttributes := target != nil && isSingleLabelTrailingDotHost(target.Hostname())
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		if rejectDomainAttributes && cookie.Domain != "" {
			// tough-cookie rejects explicit Domain attributes on a single-label
			// trailing-dot host. Go would strip the dot and accept localhost or
			// invalid as an exact domain unless this check happens first.
			continue
		}
		normalizedCookie := *cookie
		if address, err := netip.ParseAddr(normalizedCookie.Domain); err == nil && address.Is6() {
			normalizedCookie.Domain = address.String()
		}
		normalized = append(normalized, &normalizedCookie)
	}
	return normalized
}

func (j *toughCookieJar) SetCookies(target *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()

	storage, context := j.storage(target)
	if storage == nil {
		return
	}
	for _, cookie := range normalizedToughCookieJarCookies(target, cookies) {
		cookieStorage, cookieContext := storage, context
		if specialContext, ok := toughCookieBareSpecialUseContext(context, cookie); ok {
			cookieStorage, cookieContext = j.regular, specialContext
		}
		// Process one cookie at a time so mixed ordinary/special replacement and
		// deletion retain the response's exact Set-Cookie order.
		cookieStorage.SetCookies(cookieContext, []*http.Cookie{cookie})
	}
}

func toughCookieBareSpecialUseContext(target *url.URL, cookie *http.Cookie) (*url.URL, bool) {
	if target == nil || cookie == nil {
		return nil, false
	}
	domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
	if domain != "localhost" && domain != "invalid" {
		return nil, false
	}
	host := strings.ToLower(target.Hostname())
	if strings.HasSuffix(host, ".") || host != domain && !strings.HasSuffix(host, "."+domain) {
		return nil, false
	}
	context := *target
	context.Host = domain
	if port := target.Port(); port != "" {
		context.Host = net.JoinHostPort(domain, port)
	}
	return &context, true
}

func (j *toughCookieJar) Cookies(target *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()

	storage, context := j.storage(target)
	if storage == nil {
		return nil
	}
	return storage.Cookies(context)
}

func cookieContextURL(target *url.URL) *url.URL {
	if target == nil {
		return nil
	}
	context := *target
	// tough-cookie receives a URL string and applies decodeURI to its WHATWG
	// pathname. That decodes unreserved escapes while retaining escapes for
	// URI syntax such as %2F. Malformed escapes leave the complete pathname
	// unchanged because tough-cookie catches decodeURI's exception.
	context.Path = decodeURICookiePath(rawWHATWGPath(target))
	context.RawPath = ""
	return &context
}

func decodeURICookiePath(path string) string {
	const reserved = ";/?:@&=+$,#"
	var decoded strings.Builder
	for index := 0; index < len(path); {
		if path[index] != '%' {
			decoded.WriteByte(path[index])
			index++
			continue
		}
		if index+2 >= len(path) || !isASCIIHexDigit(path[index+1]) || !isASCIIHexDigit(path[index+2]) {
			return path
		}
		value := unhex(path[index+1])<<4 | unhex(path[index+2])
		if value < utf8.RuneSelf && strings.ContainsRune(reserved, rune(value)) {
			decoded.WriteString(path[index : index+3])
			index += 3
			continue
		}
		if value < utf8.RuneSelf {
			decoded.WriteByte(value)
			index += 3
			continue
		}

		encoded := make([]byte, 0, utf8.UTFMax)
		for index+2 < len(path) && path[index] == '%' &&
			isASCIIHexDigit(path[index+1]) && isASCIIHexDigit(path[index+2]) {
			encoded = append(encoded, unhex(path[index+1])<<4|unhex(path[index+2]))
			index += 3
			if utf8.FullRune(encoded) {
				break
			}
		}
		decodedRune, size := utf8.DecodeRune(encoded)
		if decodedRune == utf8.RuneError && size == 1 || size != len(encoded) {
			return path
		}
		decoded.Write(encoded)
	}
	return decoded.String()
}

func defaultCookiePath(target *url.URL) string {
	if target == nil || target.Path == "" || target.Path[0] != '/' {
		return "/"
	}
	lastSlash := strings.LastIndexByte(target.Path, '/')
	if lastSlash <= 0 {
		return "/"
	}
	return target.Path[:lastSlash]
}

func cookiePrefixAccepted(target *url.URL, cookie *http.Cookie) bool {
	if cookie == nil {
		return false
	}
	if strings.HasPrefix(cookie.Name, "__Secure-") && !cookie.Secure {
		return false
	}
	if !strings.HasPrefix(cookie.Name, "__Host-") {
		return true
	}
	path := cookie.Path
	if path == "" || path[0] != '/' {
		path = defaultCookiePath(target)
	}
	return cookie.Secure && cookie.Domain == "" && path == "/"
}

func cookieDomainAccepted(cookie *http.Cookie) bool {
	if cookie == nil || cookie.Domain == "" {
		return cookie != nil
	}
	domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
	if address, err := netip.ParseAddr(domain); err == nil {
		// tough-cookie rejects IPv4 Domain attributes but carries an explicit
		// compatibility exemption for IPv6 literals.
		return address.Is6()
	}
	if suffix, specialUse := toughCookieSpecialUsePublicSuffix(domain); specialUse {
		return suffix != ""
	}
	return collectorPublicSuffixList{}.PublicSuffix(domain) != domain
}

// toughCookieSpecialUsePublicSuffix mirrors tough-cookie 6.0.2's
// getPublicSuffix special-use branch. Its nonempty two-label result is an
// accepted registrable/store boundary; rejectPublicSuffixes rejects only the
// special-use cases for which this branch returns no suffix.
func toughCookieSpecialUsePublicSuffix(domain string) (string, bool) {
	parts := strings.Split(domain, ".")
	topLevel := parts[len(parts)-1]
	switch topLevel {
	case "local", "example", "invalid", "localhost", "test":
	default:
		return "", false
	}
	if len(parts) > 1 {
		return parts[len(parts)-2] + "." + topLevel, true
	}
	if topLevel == "localhost" || topLevel == "invalid" {
		return topLevel, true
	}
	return "", true
}

var (
	toughCookieDateDelimiter = regexp.MustCompile(`[\x09\x20-\x2F\x3B-\x40\x5B-\x60\x7B-\x7E]`)
	toughCookieTimeToken     = regexp.MustCompile(`^([0-9]{1,2}):([0-9]{1,2}):([0-9]{1,2})(?:[\x00-\x2F\x3A-\x{FF}][\x00-\x{FF}]*)?$`)
	toughCookieDayToken      = regexp.MustCompile(`^([0-9]{1,2})(?:[\x00-\x2F\x3A-\x{FF}][\x00-\x{FF}]*)?$`)
	toughCookieMonthToken    = regexp.MustCompile(`(?i)^(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[\x00-\x{FF}]*$`)
	toughCookieYearToken     = regexp.MustCompile(`^([0-9]{2,4})(?:[\x00-\x2F\x3A-\x{FF}][\x00-\x{FF}]*)?$`)
)

var toughCookieMonths = [...]string{
	"jan", "feb", "mar", "apr", "may", "jun",
	"jul", "aug", "sep", "oct", "nov", "dec",
}

const toughCookieJarValuePrefix = "\x00resthttp-tough-cookie-v1:"

func isJavaScriptWhitespace(character rune) bool {
	return character >= '\u2000' && character <= '\u200a' ||
		strings.ContainsRune("\t\n\v\f\r \u00a0\u1680\u2028\u2029\u202f\u205f\u3000\ufeff", character)
}

func trimJavaScriptWhitespace(value string) string {
	return strings.TrimFunc(value, isJavaScriptWhitespace)
}

func trimToughCookieTerminator(value string) string {
	end := len(value)
	for _, terminator := range []byte{'\n', '\r', 0} {
		if index := strings.IndexByte(value[:end], terminator); index != -1 {
			end = index
		}
	}
	return value[:end]
}

func containsToughCookieControl(value string) bool {
	for _, character := range value {
		if character <= 0x1f {
			return true
		}
	}
	return false
}

func parseToughCookiePair(header string) (string, string, string, bool) {
	header = trimJavaScriptWhitespace(header)
	firstSemicolon := strings.IndexByte(header, ';')
	pair := header
	attributes := ""
	if firstSemicolon != -1 {
		pair = header[:firstSemicolon]
		attributes = header[firstSemicolon:]
	}
	pair = trimToughCookieTerminator(pair)
	firstEquals := strings.IndexByte(pair, '=')
	if firstEquals <= 0 {
		return "", "", "", false
	}
	name := trimJavaScriptWhitespace(pair[:firstEquals])
	value := trimJavaScriptWhitespace(pair[firstEquals+1:])
	if containsToughCookieControl(name) || containsToughCookieControl(value) {
		return "", "", "", false
	}
	return name, value, attributes, true
}

type toughCookieAttribute struct {
	name  string
	value string
}

func parseToughCookieAttributes(attributes string) []toughCookieAttribute {
	parsed := make([]toughCookieAttribute, 0)
	for _, rawAttribute := range strings.Split(attributes, ";") {
		attribute := trimJavaScriptWhitespace(rawAttribute)
		if attribute == "" {
			continue
		}
		name, value, _ := strings.Cut(attribute, "=")
		parsed = append(parsed, toughCookieAttribute{
			name:  strings.ToLower(trimJavaScriptWhitespace(name)),
			value: trimJavaScriptWhitespace(value),
		})
	}
	return parsed
}

func toughCookieJarValue(value string) string {
	// NUL cannot occur in a parsed tough-cookie value, so the marker cannot
	// collide with origin-controlled input. The standard jar keeps this opaque
	// while continuing to own domain, path, expiry, and creation-order policy.
	return toughCookieJarValuePrefix + value
}

func toughCookieRequestPair(cookie *http.Cookie) string {
	if cookie == nil {
		return ""
	}
	if value, encoded := strings.CutPrefix(cookie.Value, toughCookieJarValuePrefix); encoded {
		return cookie.Name + "=" + value
	}
	return cookie.String()
}

// parseToughCookieDate ports tough-cookie 6.0.2's cookie-date token grammar.
// It intentionally accepts the legacy delimiters and two-digit years that
// time.Parse's HTTP layouts do not cover.
func parseToughCookieDate(cookieDate string) (time.Time, bool) {
	if cookieDate == "" {
		return time.Time{}, false
	}
	var (
		hour, minute, second int
		day, month, year     int
		foundTime            bool
		foundDay             bool
		foundMonth           bool
		foundYear            bool
	)
	for _, token := range toughCookieDateDelimiter.Split(cookieDate, -1) {
		if token == "" {
			continue
		}
		if !foundTime {
			if match := toughCookieTimeToken.FindStringSubmatch(token); match != nil {
				hour, _ = strconv.Atoi(match[1])
				minute, _ = strconv.Atoi(match[2])
				second, _ = strconv.Atoi(match[3])
				foundTime = true
				continue
			}
		}
		if !foundDay {
			if match := toughCookieDayToken.FindStringSubmatch(token); match != nil {
				day, _ = strconv.Atoi(match[1])
				foundDay = true
				continue
			}
		}
		if !foundMonth {
			if match := toughCookieMonthToken.FindStringSubmatch(token); match != nil {
				prefix := strings.ToLower(match[1])
				for index, candidate := range toughCookieMonths {
					if prefix == candidate {
						month = index + 1
						foundMonth = true
						break
					}
				}
				if foundMonth {
					continue
				}
			}
		}
		if !foundYear {
			if match := toughCookieYearToken.FindStringSubmatch(token); match != nil {
				year, _ = strconv.Atoi(match[1])
				foundYear = true
			}
		}
	}
	if 70 <= year && year <= 99 {
		year += 1900
	} else if foundYear && year <= 69 {
		year += 2000
	}
	if !foundTime || !foundDay || !foundMonth || !foundYear ||
		day < 1 || day > 31 || year < 1601 ||
		hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, false
	}
	parsed := time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC)
	if parsed.Year() != year || int(parsed.Month()) != month || parsed.Day() != day {
		return time.Time{}, false
	}
	return parsed, true
}

// parseToughCookieMaxAge ports tough-cookie's /^-?[0-9]+$/ plus JavaScript
// parseInt behavior. Go's jar cannot represent tough-cookie's complete finite
// number range, so large finite positive values are clamped to the longest
// duration it can safely retain. JavaScript numeric infinity remains a
// delete-now value, matching tough-cookie 6.0.2's expiryTime behavior.
func parseToughCookieMaxAge(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	start := 0
	if value[0] == '-' {
		start = 1
	}
	if start == len(value) {
		return 0, false
	}
	for index := start; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil && !math.IsInf(parsed, 0) {
		return 0, false
	}
	if math.IsInf(parsed, 0) || parsed <= 0 {
		return -1, true
	}
	maximum := int64(math.MaxInt64 / int64(time.Second))
	if intMaximum := int64(^uint(0) >> 1); intMaximum < maximum {
		maximum = intMaximum
	}
	if parsed >= float64(maximum) {
		return int(maximum), true
	}
	return int(parsed), true
}

func parseToughCookieSetCookie(header string, fromProductionWire bool) *http.Cookie {
	if fromProductionWire {
		// Undici exposes response header bytes as an isomorphic Latin-1 JS
		// string. Mapping every byte before parsing also lets the request-side
		// serializer reproduce UTF-8-looking byte sequences without decoding them.
		header = latin1HeaderValue(header)
	}
	name, value, attributes, ok := parseToughCookiePair(header)
	if !ok {
		return nil
	}
	cookie := &http.Cookie{
		Name:   name,
		Value:  toughCookieJarValue(value),
		Quoted: false,
		Raw:    header,
	}
	for _, attribute := range parseToughCookieAttributes(attributes) {
		switch attribute.name {
		case "expires":
			if parsed, ok := parseToughCookieDate(attribute.value); ok {
				cookie.Expires = parsed
				cookie.RawExpires = attribute.value
			}
		case "max-age":
			if parsed, ok := parseToughCookieMaxAge(attribute.value); ok {
				cookie.MaxAge = parsed
			}
		case "domain":
			if attribute.value == "" {
				continue
			}
			domain := strings.TrimPrefix(attribute.value, ".")
			if domain != "" {
				cookie.Domain = strings.ToLower(domain)
			}
		case "path":
			cookie.Path = ""
			if attribute.value != "" && attribute.value[0] == '/' {
				cookie.Path = attribute.value
			}
		case "secure":
			cookie.Secure = true
		case "httponly":
			cookie.HttpOnly = true
		case "samesite":
			switch strings.ToLower(attribute.value) {
			case "strict":
				cookie.SameSite = http.SameSiteStrictMode
			case "lax":
				cookie.SameSite = http.SameSiteLaxMode
			case "none":
				cookie.SameSite = http.SameSiteNoneMode
			default:
				cookie.SameSite = http.SameSiteDefaultMode
			}
		}
	}
	return cookie
}

// acceptedResponseCookies parses each raw Set-Cookie header independently,
// matching CookieJar.setCookieSync(..., {ignoreError:true}). It deliberately
// does not use http.Response.Cookies, whose all-or-nothing count limit and
// expiry parsing discard information needed for tough-cookie parity.
func acceptedResponseCookies(
	target *url.URL,
	headers http.Header,
	fromProductionWire bool,
) []*http.Cookie {
	accepted := make([]*http.Cookie, 0)
	for name, values := range headers {
		if !strings.EqualFold(name, "set-cookie") {
			continue
		}
		for _, header := range values {
			cookie := parseToughCookieSetCookie(header, fromProductionWire)
			if cookie == nil {
				continue
			}
			if cookie.Domain == "." {
				// tough-cookie's ignoreError mode treats Domain=. as if the Domain
				// attribute were absent; Go's jar rejects it unless normalized first.
				copy := *cookie
				copy.Domain = ""
				cookie = &copy
			}
			if cookieDomainAccepted(cookie) && cookiePrefixAccepted(target, cookie) {
				accepted = append(accepted, cookie)
			}
		}
	}
	return accepted
}
