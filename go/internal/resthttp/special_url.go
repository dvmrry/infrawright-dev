package resthttp

import (
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

var whatwgIDNA = idna.New(
	idna.MapForLookup(),
	idna.Transitional(false),
	idna.StrictDomainName(false),
	idna.CheckHyphens(false),
	idna.CheckJoiners(true),
	idna.BidiRule(),
	idna.VerifyDNSLength(false),
)

func hasPostUnicode15IDNADelta(input string) bool {
	// Between the pinned Unicode 15 and Node's Unicode 16 IDNA mapping tables,
	// U+1E9E is the only code point accepted by both whose mapping changed:
	// "ss" became U+00DF. New Unicode-16 code points remain disallowed by the
	// pinned x/net table and therefore already fail closed.
	return strings.ContainsRune(input, '\u1e9e')
}

func mapsToEmptyACEPrefix(label string) bool {
	// Prefixing prevents x/net from interpreting xn-- as an A-label while still
	// applying UTS-46 mapping. This catches width/case/ignored-character forms
	// that would otherwise decode to an empty label without an error.
	mapped, _ := whatwgIDNA.ToASCII("a" + label)
	return mapped == "axn--"
}

func containsForbiddenWHATWGDomainCodePoint(input string) bool {
	for _, value := range input {
		if value <= 0x20 || value == 0x7f || strings.ContainsRune("\"#%/:<>?@[\\]^`{|}", value) {
			return true
		}
	}
	return false
}

func preprocessWHATWGURL(input string) string {
	input = strings.TrimFunc(input, func(value rune) bool { return value <= 0x20 })
	return strings.Map(func(value rune) rune {
		if value == '\t' || value == '\n' || value == '\r' {
			return -1
		}
		return value
	}, input)
}

func splitURLScheme(input string) (string, string, bool) {
	colon := strings.IndexByte(input, ':')
	if colon <= 0 {
		return "", input, false
	}
	for index := 0; index < colon; index++ {
		value := input[index]
		if index == 0 {
			if !isASCIIAlpha(value) {
				return "", input, false
			}
		} else if !isASCIIAlpha(value) && !isASCIIDigit(value) &&
			value != '+' && value != '-' && value != '.' {
			return "", input, false
		}
	}
	return strings.ToLower(input[:colon]), input[colon+1:], true
}

func isASCIIAlpha(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func normalizeSpecialBackslashes(input string) string {
	end := len(input)
	if delimiter := strings.IndexAny(input, "?#"); delimiter >= 0 {
		end = delimiter
	}
	return strings.ReplaceAll(input[:end], "\\", "/") + input[end:]
}

func normalizeSpecialReference(input string, base *url.URL) string {
	if base != nil && (base.Scheme == "http" || base.Scheme == "https") {
		input = normalizeSpecialBackslashes(input)
		if strings.HasPrefix(input, "//") {
			return "//" + strings.TrimLeft(input, "/")
		}
	}
	scheme, remainder, hasScheme := splitURLScheme(input)
	if !hasScheme || scheme != "http" && scheme != "https" {
		return input
	}
	remainder = normalizeSpecialBackslashes(remainder)
	if base != nil && strings.EqualFold(scheme, base.Scheme) && !strings.HasPrefix(remainder, "//") {
		if _, _, parsesAsScheme := splitURLScheme(remainder); parsesAsScheme {
			return "./" + remainder
		}
		return remainder
	}
	return scheme + "://" + strings.TrimLeft(remainder, "/")
}

func normalizeWHATWGAuthorityHost(input string) (string, error) {
	authorityMarker := 0
	scheme, remainder, hasScheme := splitURLScheme(input)
	if hasScheme {
		if scheme != "http" && scheme != "https" {
			return input, nil
		}
		authorityMarker = len(input) - len(remainder)
	}
	if !strings.HasPrefix(input[authorityMarker:], "//") {
		return input, nil
	}
	authorityStart := authorityMarker + 2
	authorityEnd := len(input)
	if delimiter := strings.IndexAny(input[authorityStart:], "/?#"); delimiter >= 0 {
		authorityEnd = authorityStart + delimiter
	}
	authority := input[authorityStart:authorityEnd]
	userinfo := ""
	if separator := strings.LastIndexByte(authority, '@'); separator >= 0 {
		userinfo = netURLSafeUserinfo(authority[:separator]) + "@"
		authority = authority[separator+1:]
	}

	host := authority
	port := ""
	bracketed := strings.HasPrefix(authority, "[")
	if bracketed {
		closing := strings.IndexByte(authority, ']')
		if closing < 0 {
			return "", errors.New("IPv6 host is missing a closing bracket")
		}
		host = authority[1:closing]
		port = authority[closing+1:]
	} else if colon := strings.LastIndexByte(authority, ':'); colon >= 0 {
		host = authority[:colon]
		port = authority[colon:]
	}
	if bracketed && strings.Contains(host, "%") {
		return "", errors.New("percent encoding is not valid inside an IPv6 host")
	}
	decoded, err := url.PathUnescape(host)
	if err != nil {
		return "", err
	}
	if !bracketed && strings.Contains(decoded, ":") {
		return "", errors.New("non-IPv6 host contains a colon")
	}
	canonical, err := canonicalWHATWGHostname(decoded)
	if err != nil {
		return "", err
	}
	if bracketed && !strings.HasPrefix(canonical, "[") {
		return "", errors.New("bracketed host is not IPv6")
	}
	return input[:authorityStart] + userinfo + canonical + port + input[authorityEnd:], nil
}

func netURLSafeUserinfo(input string) string {
	const hex = "0123456789ABCDEF"
	var output strings.Builder
	for index := 0; index < len(input); index++ {
		value := input[index]
		if value == '%' {
			if index+2 < len(input) && isASCIIHexDigit(input[index+1]) && isASCIIHexDigit(input[index+2]) {
				output.WriteString(input[index : index+3])
				index += 2
				continue
			}
			output.WriteString("%25")
			continue
		}
		if value <= 0x20 || value == 0x7f {
			output.WriteByte('%')
			output.WriteByte(hex[value>>4])
			output.WriteByte(hex[value&15])
			continue
		}
		output.WriteByte(value)
	}
	return output.String()
}

func isASCIIHexDigit(value byte) bool {
	return isASCIIDigit(value) || value >= 'A' && value <= 'F' || value >= 'a' && value <= 'f'
}

func canonicalWHATWGProxyUserinfo(input string) (string, bool) {
	authorityMarker := 0
	scheme, remainder, hasScheme := splitURLScheme(input)
	if hasScheme {
		if scheme != "http" && scheme != "https" {
			return "", false
		}
		authorityMarker = len(input) - len(remainder)
	}
	if !strings.HasPrefix(input[authorityMarker:], "//") {
		return "", false
	}
	authorityStart := authorityMarker + 2
	authorityEnd := len(input)
	if delimiter := strings.IndexAny(input[authorityStart:], "/?#"); delimiter >= 0 {
		authorityEnd = authorityStart + delimiter
	}
	authority := input[authorityStart:authorityEnd]
	separator := strings.LastIndexByte(authority, '@')
	if separator < 0 {
		return "", false
	}
	raw := authority[:separator]
	username := raw
	password := ""
	if colon := strings.IndexByte(raw, ':'); colon >= 0 {
		username = raw[:colon]
		password = raw[colon+1:]
	}
	encode := func(value string) string {
		return percentEncodeWHATWGBytes(value, func(character byte) bool {
			return isC0PercentEncoded(character) || strings.ContainsRune("\"#<>?`{}/:;=@[\\]^|", rune(character))
		})
	}
	username = encode(username)
	password = encode(password)
	if username == "" && password == "" {
		return "", false
	}
	if password != "" {
		return username + ":" + password, true
	}
	return username, true
}

func normalizeWHATWGDotSegments(input string) string {
	pathEnd := len(input)
	if delimiter := strings.IndexAny(input, "?#"); delimiter >= 0 {
		pathEnd = delimiter
	}

	pathStart := 0
	_, remainder, hasScheme := splitURLScheme(input)
	if hasScheme {
		pathStart = len(input) - len(remainder)
	}
	if strings.HasPrefix(input[pathStart:pathEnd], "//") {
		authorityStart := pathStart + 2
		separator := strings.IndexByte(input[authorityStart:pathEnd], '/')
		if separator < 0 {
			return input
		}
		pathStart = authorityStart + separator
	}

	segments := strings.Split(input[pathStart:pathEnd], "/")
	changed := false
	for index, segment := range segments {
		switch strings.ToLower(segment) {
		case "%2e":
			segments[index] = "."
			changed = true
		case ".%2e", "%2e.", "%2e%2e":
			segments[index] = ".."
			changed = true
		}
	}
	if !changed {
		return input
	}
	return input[:pathStart] + strings.Join(segments, "/") + input[pathEnd:]
}

func whatwgPathBounds(input string) (int, int) {
	pathEnd := len(input)
	if delimiter := strings.IndexAny(input, "?#"); delimiter >= 0 {
		pathEnd = delimiter
	}
	pathStart := 0
	_, remainder, hasScheme := splitURLScheme(input)
	if hasScheme {
		pathStart = len(input) - len(remainder)
	}
	if strings.HasPrefix(input[pathStart:pathEnd], "//") {
		authorityStart := pathStart + 2
		separator := strings.IndexByte(input[authorityStart:pathEnd], '/')
		if separator < 0 {
			return pathEnd, pathEnd
		}
		pathStart = authorityStart + separator
	}
	return pathStart, pathEnd
}

func netURLSafePath(path string) string {
	var output strings.Builder
	for index := 0; index < len(path); index++ {
		if path[index] == '%' &&
			(index+2 >= len(path) || !isASCIIHexDigit(path[index+1]) || !isASCIIHexDigit(path[index+2])) {
			output.WriteString("%25")
			continue
		}
		output.WriteByte(path[index])
	}
	return output.String()
}

func netURLSafeReference(input string) string {
	pathStart, pathEnd := whatwgPathBounds(input)
	output := input[:pathStart] + netURLSafePath(input[pathStart:pathEnd]) + input[pathEnd:]
	if fragment := strings.IndexByte(output, '#'); fragment >= 0 {
		output = output[:fragment+1] + netURLSafePath(output[fragment+1:])
	}
	if hostStart, hostEnd, ok := whatwgAuthorityHostBounds(output); ok {
		var safeHost strings.Builder
		safeHost.Grow(hostEnd - hostStart)
		for index := hostStart; index < hostEnd; index++ {
			switch output[index] {
			case '"', '`', '{', '}':
				safeHost.WriteByte('x')
			default:
				safeHost.WriteByte(output[index])
			}
		}
		output = output[:hostStart] + safeHost.String() + output[hostEnd:]
	}
	if _, _, hasScheme := splitURLScheme(output); !hasScheme && !strings.HasPrefix(output, "//") {
		pathStart, pathEnd := whatwgPathBounds(output)
		firstSegment := output[pathStart:pathEnd]
		if slash := strings.IndexByte(firstSegment, '/'); slash >= 0 {
			firstSegment = firstSegment[:slash]
		}
		if strings.Contains(firstSegment, ":") {
			output = "./" + output
		}
	}
	return output
}

func whatwgAuthorityHostBounds(input string) (int, int, bool) {
	authorityMarker := 0
	scheme, remainder, hasScheme := splitURLScheme(input)
	if hasScheme {
		if scheme != "http" && scheme != "https" {
			return 0, 0, false
		}
		authorityMarker = len(input) - len(remainder)
	}
	if !strings.HasPrefix(input[authorityMarker:], "//") {
		return 0, 0, false
	}
	authorityStart := authorityMarker + 2
	authorityEnd := len(input)
	if delimiter := strings.IndexAny(input[authorityStart:], "/?#"); delimiter >= 0 {
		authorityEnd = authorityStart + delimiter
	}
	hostStart := authorityStart
	if separator := strings.LastIndexByte(input[authorityStart:authorityEnd], '@'); separator >= 0 {
		hostStart = authorityStart + separator + 1
	}
	return hostStart, authorityEnd, true
}

func decodeWHATWGPath(path string) string {
	var output strings.Builder
	for index := 0; index < len(path); index++ {
		if path[index] == '%' && index+2 < len(path) &&
			isASCIIHexDigit(path[index+1]) && isASCIIHexDigit(path[index+2]) {
			output.WriteByte(unhex(path[index+1])<<4 | unhex(path[index+2]))
			index += 2
			continue
		}
		output.WriteByte(path[index])
	}
	return output.String()
}

func unhex(value byte) byte {
	if value >= '0' && value <= '9' {
		return value - '0'
	}
	if value >= 'A' && value <= 'F' {
		return value - 'A' + 10
	}
	return value - 'a' + 10
}

func removeWHATWGDotSegments(path string) string {
	absolute := strings.HasPrefix(path, "/")
	parts := strings.Split(path, "/")
	output := make([]string, 0, len(parts))
	for index, part := range parts {
		if index == 0 && absolute {
			output = append(output, "")
			continue
		}
		switch part {
		case ".":
			if index == len(parts)-1 {
				output = append(output, "")
			}
		case "..":
			minimum := 0
			if absolute {
				minimum = 1
			}
			if len(output) > minimum {
				output = output[:len(output)-1]
			}
			if index == len(parts)-1 {
				output = append(output, "")
			}
		default:
			output = append(output, part)
		}
	}
	resolved := strings.Join(output, "/")
	if absolute && resolved == "" {
		return "/"
	}
	return resolved
}

func rawWHATWGPath(input *url.URL) string {
	if input == nil {
		return ""
	}
	if input.RawPath != "" {
		return input.RawPath
	}
	return input.EscapedPath()
}

func resolveWHATWGRawPath(base, reference *url.URL, referencePath string) string {
	if base == nil || reference.Scheme != "" || reference.Host != "" {
		return removeWHATWGDotSegments(referencePath)
	}
	if referencePath == "" {
		return rawWHATWGPath(base)
	}
	if strings.HasPrefix(referencePath, "/") {
		return removeWHATWGDotSegments(referencePath)
	}
	basePath := rawWHATWGPath(base)
	separator := strings.LastIndexByte(basePath, '/')
	if separator >= 0 {
		basePath = basePath[:separator+1]
	} else {
		basePath = ""
	}
	return removeWHATWGDotSegments(basePath + referencePath)
}

func whatwgURLString(input *url.URL, includeFragment bool) string {
	if input == nil || input.Scheme != "http" && input.Scheme != "https" {
		if input == nil {
			return ""
		}
		return input.String()
	}
	var output strings.Builder
	output.WriteString(input.Scheme)
	output.WriteString("://")
	if input.User != nil {
		output.WriteString(input.User.String())
		output.WriteByte('@')
	}
	output.WriteString(input.Host)
	path := rawWHATWGPath(input)
	if path == "" {
		path = "/"
	}
	output.WriteString(path)
	if input.ForceQuery || input.RawQuery != "" {
		output.WriteByte('?')
		output.WriteString(input.RawQuery)
	}
	if includeFragment && (input.Fragment != "" || input.RawFragment != "") {
		output.WriteByte('#')
		if input.RawFragment != "" {
			output.WriteString(input.RawFragment)
		} else {
			output.WriteString(input.EscapedFragment())
		}
	}
	return output.String()
}

func percentEncodeWHATWGBytes(input string, shouldEncode func(byte) bool) string {
	const hex = "0123456789ABCDEF"
	var output strings.Builder
	for _, value := range []byte(input) {
		if !shouldEncode(value) {
			output.WriteByte(value)
			continue
		}
		output.WriteByte('%')
		output.WriteByte(hex[value>>4])
		output.WriteByte(hex[value&15])
	}
	return output.String()
}

func isC0PercentEncoded(value byte) bool {
	return value <= 0x20 || value > 0x7e
}

func normalizeWHATWGPercentEncoding(input string) string {
	fragmentStart := strings.IndexByte(input, '#')
	queryStart := strings.IndexByte(input, '?')
	if fragmentStart >= 0 && queryStart > fragmentStart {
		queryStart = -1
	}
	pathEnd := len(input)
	if queryStart >= 0 {
		pathEnd = queryStart
	} else if fragmentStart >= 0 {
		pathEnd = fragmentStart
	}

	pathStart := 0
	_, remainder, hasScheme := splitURLScheme(input)
	if hasScheme {
		pathStart = len(input) - len(remainder)
	}
	if strings.HasPrefix(input[pathStart:pathEnd], "//") {
		authorityStart := pathStart + 2
		separator := strings.IndexByte(input[authorityStart:pathEnd], '/')
		if separator < 0 {
			pathStart = pathEnd
		} else {
			pathStart = authorityStart + separator
		}
	}

	var output strings.Builder
	output.WriteString(input[:pathStart])
	output.WriteString(percentEncodeWHATWGBytes(input[pathStart:pathEnd], func(value byte) bool {
		return isC0PercentEncoded(value) || value == '"' || value == '<' || value == '>' ||
			value == '^' || value == '`' || value == '{' || value == '}'
	}))
	if queryStart >= 0 {
		queryEnd := len(input)
		if fragmentStart >= 0 {
			queryEnd = fragmentStart
		}
		output.WriteByte('?')
		output.WriteString(percentEncodeWHATWGBytes(input[queryStart+1:queryEnd], func(value byte) bool {
			return isC0PercentEncoded(value) || value == '"' || value == '\'' || value == '<' || value == '>'
		}))
	}
	if fragmentStart >= 0 {
		output.WriteByte('#')
		output.WriteString(percentEncodeWHATWGBytes(input[fragmentStart+1:], func(value byte) bool {
			return isC0PercentEncoded(value) || value == '"' || value == '<' || value == '>' || value == '`'
		}))
	}
	return output.String()
}

func parseWHATWGIPv4Number(input string) (uint64, bool) {
	if input == "" {
		return 0, false
	}
	base := 10
	digits := input
	if len(digits) >= 2 && digits[0] == '0' && (digits[1] == 'x' || digits[1] == 'X') {
		base = 16
		digits = digits[2:]
	} else if len(digits) >= 2 && digits[0] == '0' {
		base = 8
		digits = digits[1:]
	}
	if digits == "" {
		return 0, true
	}
	value, err := strconv.ParseUint(digits, base, 64)
	return value, err == nil
}

func isASCIIDigits(input string) bool {
	if input == "" {
		return false
	}
	for _, value := range []byte(input) {
		if value < '0' || value > '9' {
			return false
		}
	}
	return true
}

func isASCIIHexNumber(input string) bool {
	if len(input) <= 2 || input[0] != '0' || input[1] != 'x' && input[1] != 'X' {
		return false
	}
	for _, value := range []byte(input[2:]) {
		if !isASCIIDigit(value) && (value < 'A' || value > 'F') && (value < 'a' || value > 'f') {
			return false
		}
	}
	return true
}

func canonicalWHATWGIPv4(hostname string) (string, bool, error) {
	trimmed := strings.TrimSuffix(hostname, ".")
	parts := strings.Split(trimmed, ".")
	if len(parts) == 0 {
		return "", false, nil
	}
	if _, numeric := parseWHATWGIPv4Number(parts[len(parts)-1]); !numeric &&
		!isASCIIDigits(parts[len(parts)-1]) && !isASCIIHexNumber(parts[len(parts)-1]) {
		return "", false, nil
	}
	if len(parts) > 4 {
		return "", true, errors.New("too many IPv4 components")
	}
	numbers := make([]uint64, len(parts))
	for index, part := range parts {
		value, ok := parseWHATWGIPv4Number(part)
		if !ok {
			return "", true, errors.New("invalid IPv4 component")
		}
		numbers[index] = value
	}
	for _, value := range numbers[:len(numbers)-1] {
		if value > 255 {
			return "", true, errors.New("IPv4 component exceeds 255")
		}
	}
	maximumLast := uint64(1) << uint(8*(5-len(numbers)))
	if numbers[len(numbers)-1] >= maximumLast {
		return "", true, errors.New("IPv4 address exceeds 32 bits")
	}
	address := numbers[len(numbers)-1]
	for index, value := range numbers[:len(numbers)-1] {
		address += value << uint(8*(3-index))
	}
	return strconv.FormatUint(address>>24&255, 10) + "." +
		strconv.FormatUint(address>>16&255, 10) + "." +
		strconv.FormatUint(address>>8&255, 10) + "." +
		strconv.FormatUint(address&255, 10), true, nil
}

func canonicalWHATWGHostname(hostname string) (string, error) {
	if strings.Contains(hostname, "%") {
		return "", errors.New("IPv6 zones are not valid WHATWG hosts")
	}
	if address, err := netip.ParseAddr(hostname); err == nil {
		if address.Is6() {
			if address.Is4In6() {
				bytes := address.As16()
				return "[::ffff:" + strconv.FormatUint(uint64(bytes[12])<<8|uint64(bytes[13]), 16) +
					":" + strconv.FormatUint(uint64(bytes[14])<<8|uint64(bytes[15]), 16) + "]", nil
			}
			return "[" + address.String() + "]", nil
		}
		return address.String(), nil
	}
	if containsForbiddenWHATWGDomainCodePoint(hostname) {
		return "", errors.New("invalid domain code point")
	}
	if hasPostUnicode15IDNADelta(hostname) {
		return "", errors.New("hostname requires a post-Unicode-15 IDNA mapping")
	}
	for _, label := range strings.FieldsFunc(hostname, func(value rune) bool {
		return value == '.' || value == '\u3002' || value == '\uff0e' || value == '\uff61'
	}) {
		if mapsToEmptyACEPrefix(label) {
			return "", errors.New("invalid empty A-label")
		}
	}
	hostname, err := whatwgIDNA.ToASCII(hostname)
	if err != nil {
		return "", err
	}
	if containsForbiddenWHATWGDomainCodePoint(hostname) {
		return "", errors.New("IDNA mapping produced a forbidden domain code point")
	}
	if hostname == "" {
		return "", errors.New("IDNA mapping produced an empty host")
	}
	if ipv4, considered, err := canonicalWHATWGIPv4(hostname); considered {
		return ipv4, err
	}
	return hostname, nil
}

func canonicalizeWHATWGHTTPURL(parsed *url.URL) (*url.URL, error) {
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return parsed, nil
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("special URL has no host")
	}
	hostname, err := canonicalWHATWGHostname(parsed.Hostname())
	if err != nil {
		return nil, err
	}
	port := parsed.Port()
	if port != "" {
		value, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil {
			return nil, parseErr
		}
		port = strconv.FormatUint(value, 10)
		if parsed.Scheme == "http" && port == "80" || parsed.Scheme == "https" && port == "443" {
			port = ""
		}
	}
	parsed.Host = hostname
	if port != "" {
		parsed.Host += ":" + port
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed, nil
}

func parseWHATWGURLReference(input string, base *url.URL) (*url.URL, string, error) {
	preprocessed := preprocessWHATWGURL(input)
	normalized := normalizeSpecialReference(preprocessed, base)
	normalized, authorityErr := normalizeWHATWGAuthorityHost(normalized)
	if authorityErr != nil {
		return nil, preprocessed, authorityErr
	}
	normalized = normalizeWHATWGDotSegments(normalized)
	normalized = normalizeWHATWGPercentEncoding(normalized)
	pathStart, pathEnd := whatwgPathBounds(normalized)
	referencePath := normalized[pathStart:pathEnd]
	rawFragment := ""
	if fragment := strings.IndexByte(normalized, '#'); fragment >= 0 {
		rawFragment = normalized[fragment+1:]
	}
	reference, err := url.Parse(netURLSafeReference(normalized))
	if err != nil {
		return nil, preprocessed, err
	}
	if strings.HasPrefix(normalized, "//") && reference.Host == "" {
		return nil, preprocessed, errors.New("special URL has no host")
	}
	if hostStart, hostEnd, ok := whatwgAuthorityHostBounds(normalized); ok {
		reference.Host = normalized[hostStart:hostEnd]
	}
	resolved := reference
	if base != nil {
		resolved = base.ResolveReference(reference)
	} else if reference.Scheme == "http" || reference.Scheme == "https" {
		root := &url.URL{Scheme: reference.Scheme, Host: reference.Host, Path: "/"}
		resolved = root.ResolveReference(reference)
	}
	canonical, err := canonicalizeWHATWGHTTPURL(resolved)
	if err != nil {
		return nil, preprocessed, err
	}
	rawPath := resolveWHATWGRawPath(base, reference, referencePath)
	if rawPath == "" && canonical.Scheme != "" && canonical.Host != "" {
		rawPath = "/"
	}
	canonical.Path = decodeWHATWGPath(rawPath)
	canonical.RawPath = rawPath
	if strings.Contains(normalized, "#") {
		canonical.RawFragment = rawFragment
	} else {
		canonical.RawFragment = ""
	}
	return canonical, preprocessed, err
}
