package resthttp

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

func selectedEnvironment(environment collectors.Environment, lower, upper string) string {
	if value, ok := environment[lower]; ok {
		return value
	}
	if value, ok := environment[upper]; ok {
		return value
	}
	return ""
}

func validProxyURL(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parsed, preprocessed, err := parseWHATWGURLReference(value, nil)
	if err != nil || parsed.Scheme == "" {
		return "", ioFailure(
			"INVALID_REST_PROXY",
			"HTTP proxy configuration must be an http:// or https:// URL",
		)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Opaque != "" || parsed.Hostname() == "" ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", ioFailure(
			"INVALID_REST_PROXY",
			"HTTP proxy configuration must be an http:// or https:// host URL",
		)
	}

	parsed.Path = "/"
	parsed.RawPath = ""
	normalizedInput := normalizeSpecialReference(preprocessed, nil)
	userinfo, hasUserinfo := canonicalWHATWGProxyUserinfo(normalizedInput)
	parsed.User = nil
	canonical := parsed.String()
	if hasUserinfo {
		prefix := parsed.Scheme + "://"
		canonical = prefix + userinfo + "@" + strings.TrimPrefix(canonical, prefix)
	}
	if strings.HasSuffix(preprocessed, "#") && !strings.HasSuffix(canonical, "#") {
		canonical += "#"
	}
	return canonical, nil
}

func validProxyCredentials(userinfo string) bool {
	username, password, hasPassword := strings.Cut(userinfo, ":")
	if username == "" || !hasPassword || password == "" {
		return true
	}
	for _, component := range []string{username, password} {
		decoded, err := url.PathUnescape(component)
		if err != nil || !utf8.ValidString(decoded) {
			return false
		}
	}
	return true
}

// SnapshotRestProxyEnvironment ports snapshotRestProxyEnvironment from
// node-src/io/rest-http-transport.ts, including own-property-style lowercase
// precedence and explicit empty values.
func SnapshotRestProxyEnvironment(environment collectors.Environment) (RestProxyEnvironment, error) {
	httpValue := selectedEnvironment(environment, "http_proxy", "HTTP_PROXY")
	httpsValue := selectedEnvironment(environment, "https_proxy", "HTTPS_PROXY")
	noProxy := selectedEnvironment(environment, "no_proxy", "NO_PROXY")
	httpProxy, err := validProxyURL(httpValue)
	if err != nil {
		return RestProxyEnvironment{}, err
	}
	httpsProxy, err := validProxyURL(httpsValue)
	if err != nil {
		return RestProxyEnvironment{}, err
	}
	return RestProxyEnvironment{HTTPProxy: httpProxy, HTTPSProxy: httpsProxy, NoProxy: noProxy}, nil
}

type noProxyEntry struct {
	hostname string
	port     int
}

type proxySelector struct {
	httpProxy  *url.URL
	httpsProxy *url.URL
	noProxyRaw string
	noProxy    []noProxyEntry
}

func newProxySelector(snapshot RestProxyEnvironment) (*proxySelector, error) {
	selector := &proxySelector{noProxyRaw: snapshot.NoProxy, noProxy: parseNoProxy(snapshot.NoProxy)}
	var err error
	if snapshot.HTTPProxy != "" {
		if userinfo, ok := canonicalWHATWGProxyUserinfo(snapshot.HTTPProxy); ok && !validProxyCredentials(userinfo) {
			// ProxyAgent evaluates decodeURIComponent while it constructs the
			// dispatcher. Malformed complete credentials therefore escape as the
			// raw URIError contract, not as validProxyUrl configuration failure.
			return nil, errors.New("URI malformed")
		}
		selector.httpProxy, _, err = parseWHATWGURLReference(snapshot.HTTPProxy, nil)
		if err != nil {
			return nil, err
		}
	}
	stripIncompleteProxyCredentials(selector.httpProxy)
	if snapshot.HTTPSProxy != "" {
		if userinfo, ok := canonicalWHATWGProxyUserinfo(snapshot.HTTPSProxy); ok && !validProxyCredentials(userinfo) {
			return nil, errors.New("URI malformed")
		}
		selector.httpsProxy, _, err = parseWHATWGURLReference(snapshot.HTTPSProxy, nil)
		if err != nil {
			return nil, err
		}
	}
	stripIncompleteProxyCredentials(selector.httpsProxy)
	return selector, nil
}

func stripIncompleteProxyCredentials(proxyURL *url.URL) {
	if proxyURL == nil || proxyURL.User == nil {
		return
	}
	password, hasPassword := proxyURL.User.Password()
	if proxyURL.User.Username() == "" || !hasPassword || password == "" {
		proxyURL.User = nil
	}
}

func parseNoProxy(value string) []noProxyEntry {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || isECMAScriptWhitespace(r)
	})
	entries := make([]noProxyEntry, 0, len(parts))
	for _, part := range parts {
		hostname := part
		port := 0
		if colon := strings.LastIndexByte(part, ':'); colon > 0 && colon+1 < len(part) {
			portText := part[colon+1:]
			allDigits := true
			for _, character := range portText {
				if character < '0' || character > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				if parsed, err := strconv.Atoi(portText); err == nil {
					hostname = part[:colon]
					port = parsed
				}
			}
		}
		if strings.HasPrefix(hostname, "*.") {
			hostname = strings.TrimPrefix(hostname, "*.")
		} else {
			hostname = strings.TrimPrefix(hostname, ".")
		}
		entries = append(entries, noProxyEntry{hostname: strings.ToLower(hostname), port: port})
	}
	return entries
}

func isECMAScriptWhitespace(value rune) bool {
	return value >= '\u0009' && value <= '\u000d' || value == '\u0020' || value == '\u00a0' ||
		value == '\u1680' || value >= '\u2000' && value <= '\u200a' || value == '\u2028' ||
		value == '\u2029' || value == '\u202f' || value == '\u205f' || value == '\u3000' ||
		value == '\ufeff'
}

func splitUndiciHostPort(target *url.URL) (string, int) {
	host := strings.ToLower(target.Host)
	port := 0
	if colon := strings.LastIndexByte(host, ':'); colon >= 0 {
		portText := host[colon+1:]
		allDigits := true
		for _, character := range portText {
			if character < '0' || character > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			host = host[:colon]
			if portText != "" {
				if parsed, err := strconv.Atoi(portText); err == nil {
					port = parsed
				}
			}
		}
	}
	if port == 0 {
		switch target.Scheme {
		case "http":
			port = 80
		case "https":
			port = 443
		}
	}
	return host, port
}

func (p *proxySelector) shouldProxy(target *url.URL) bool {
	if p.noProxyRaw == "*" {
		return false
	}
	hostname, port := splitUndiciHostPort(target)
	for _, entry := range p.noProxy {
		if entry.port != 0 && entry.port != port {
			continue
		}
		if hostname == entry.hostname || strings.HasSuffix(hostname, "."+entry.hostname) {
			return false
		}
	}
	return true
}

func (p *proxySelector) proxyURL(target *url.URL) (*url.URL, error) {
	if target == nil {
		return nil, errors.New("proxy target URL is nil")
	}
	if !p.shouldProxy(target) {
		return nil, nil
	}
	selected := p.httpProxy
	if target.Scheme == "https" {
		selected = p.httpsProxy
	}
	if selected == nil {
		return nil, nil
	}
	cloned := *selected
	return &cloned, nil
}
