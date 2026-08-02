package collectors

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// The HTTP transport uses this bounded 429/Retry-After schedule around each
// request. Retry-After accepts the decimal and special-value spellings defined
// below for compatibility with existing collector behavior.

const (
	maxRetries = 5
	// retryCapMs bounds an individual retry delay at 30 seconds.
	retryCapMs = 30_000.0
)

// retryAfterNumber matches the accepted Retry-After numeric vocabulary: a signed
// decimal with optional fraction and exponent, or an inf/infinity/nan spelling.
var retryAfterNumber = regexp.MustCompile(`(?i)^[+-]?(?:(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?|inf(?:inity)?|nan)$`)

// RetryDelayMs returns the bounded delay for a retry attempt. Empty or invalid
// Retry-After values use exponential backoff.
func RetryDelayMs(attempt int, retryAfter string) float64 {
	token := strings.TrimSpace(retryAfter)
	if token != "" && retryAfterNumber.MatchString(token) {
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
			// token is known to match retryAfterNumber's numeric branch here (the
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

// clampRetryDelay constrains delays to the supported non-negative range.
func clampRetryDelay(valueMs float64) float64 {
	if valueMs > retryCapMs {
		valueMs = retryCapMs
	}
	if valueMs < 0 {
		valueMs = 0
	}
	return valueMs
}

// CollectorMaxRetries returns the maximum retry count used by the transport.
func CollectorMaxRetries() int {
	return maxRetries
}
