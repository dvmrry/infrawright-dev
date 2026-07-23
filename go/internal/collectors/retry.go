package collectors

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// retry.go ports node-src/collectors/retry.ts: the bounded 429/Retry-After
// retry schedule the (not-yet-ported) HTTP transport applies around each
// request, using Python's own float-parsing vocabulary for the
// Retry-After header value.
//
// collectorMaxRetries/retryDelayMs are consumed by
// node-src/io/rest-http-transport.ts, not by anything in this package's own
// fetch engine (rest.go) -- they are ported here, and re-exported from this
// package's public surface, purely because that is where the Node source
// defines and re-exports them (rest.ts re-exports both from retry.ts) so a
// future transport implementation has them available from the same seam
// package it otherwise depends on.

const (
	maxRetries = 5
	// retryCapMs ports RETRY_CAP_MS from node-src/collectors/retry.ts.
	retryCapMs = 30_000.0
)

// pythonFloat ports the PYTHON_FLOAT regexp from
// node-src/collectors/retry.ts: the same lexical grammar Python's
// float(str) constructor accepts (a signed decimal with optional fraction
// and exponent, or a signed/unsigned "inf"/"infinity"/"nan" spelling,
// case-insensitively).
var pythonFloat = regexp.MustCompile(`(?i)^[+-]?(?:(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?|inf(?:inity)?|nan)$`)

// RetryDelayMs ports retryDelayMs from node-src/collectors/retry.ts: it
// matches Python's float parsing of a Retry-After header value and the
// collector's bounded 429 backoff schedule.
//
// retryAfter being "" is this function's Go analogue of the TS parameter
// being null, undefined, or an all-whitespace string: token.trim() == ""
// there falls through to the exponential-backoff branch exactly as an
// empty Go string does here.
func RetryDelayMs(attempt int, retryAfter string) float64 {
	token := strings.TrimSpace(retryAfter)
	if token != "" && pythonFloat.MatchString(token) {
		normalized := strings.ToLower(token)
		normalized = strings.TrimPrefix(strings.TrimPrefix(normalized, "+"), "-")
		if normalized == "nan" {
			return 0
		}
		sign := 1.0
		if strings.HasPrefix(token, "-") {
			sign = -1
		}
		var seconds float64
		if normalized == "inf" || normalized == "infinity" {
			seconds = sign * math.Inf(1)
		} else {
			// token is known to match pythonFloat's numeric branch here (the
			// inf/infinity/nan spellings are handled above), so
			// strconv.ParseFloat -- which accepts the same decimal/exponent
			// grammar -- always succeeds.
			parsed, _ := strconv.ParseFloat(token, 64)
			seconds = parsed
		}
		return clampRetryDelay(seconds * 1_000)
	}
	return math.Min(1_000*math.Pow(2, float64(attempt)), retryCapMs)
}

// clampRetryDelay ports the `Math.max(0, Math.min(seconds * 1_000,
// RETRY_CAP_MS))` clamp in retryDelayMs, including its NaN-produces-NaN
// caveat: seconds is never NaN by the time this is called (the "nan"
// literal is special-cased above and returns 0 directly, so a Retry-After
// value that survives to here always parses to a finite or infinite
// float), so this clamp's ordinary Math.min/Math.max behavior is all that
// is left to reproduce.
func clampRetryDelay(valueMs float64) float64 {
	if valueMs > retryCapMs {
		valueMs = retryCapMs
	}
	if valueMs < 0 {
		valueMs = 0
	}
	return valueMs
}

// CollectorMaxRetries ports collectorMaxRetries from
// node-src/collectors/retry.ts.
func CollectorMaxRetries() int {
	return maxRetries
}
