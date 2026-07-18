package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const (
	// ReferenceBackendVariable ports REFERENCE_BACKEND_VARIABLE from
	// node-src/domain/reference-backend.ts.
	ReferenceBackendVariable = "infrawright_remote_state_backend_config"
	// ReferenceBackendEnvironment ports REFERENCE_BACKEND_ENVIRONMENT from
	// node-src/domain/reference-backend.ts.
	ReferenceBackendEnvironment = "TF_VAR_" + ReferenceBackendVariable

	maxReferenceBackendConfigBytes = int64(64 * 1024)
)

func referenceBackendFailure(code, message string, category procerr.Category) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: category,
		Message:  message,
	})
}

func isReferenceBackendStringField(key string) bool {
	switch key {
	case "container_name", "resource_group_name", "storage_account_name", "subscription_id", "tenant_id":
		return true
	default:
		return false
	}
}

func isReferenceBackendBooleanField(key string) bool {
	switch key {
	case "lookup_blob_endpoint", "use_azuread_auth", "use_cli", "use_msi", "use_oidc":
		return true
	default:
		return false
	}
}

func invalidReferenceBackendFailure(message string) *procerr.ProcessFailure {
	return referenceBackendFailure("INVALID_REFERENCE_BACKEND_CONFIG", message, procerr.CategoryDomain)
}

func referenceBackendReadBudget() *artifacts.ReadBudget {
	limit := big.NewInt(maxReferenceBackendConfigBytes)
	budget, err := artifacts.NewReadBudget(artifacts.BoundedReadLimits{
		MaxFiles:            1,
		MaxDirectories:      1,
		MaxDirectoryEntries: 1,
		MaxDepth:            0,
		MaxTotalBytes:       limit,
		MaxFileBytes:        limit,
	})
	if err != nil {
		// The constants above are fixed valid limits; failure is unreachable.
		panic(err)
	}
	return budget
}

func invalidReferenceBackendRead(err error) bool {
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.Code {
	case "FILE_LIMIT_EXCEEDED", "INVALID_UTF8", "NOT_REGULAR_FILE":
		return true
	default:
		return false
	}
}

// ReferenceBackendEnvironmentFromConfig ports referenceBackendEnvironment
// from node-src/domain/reference-backend.ts. It projects only reviewed,
// non-secret AzureRM address and behavior fields into Terraform's environment.
func ReferenceBackendEnvironmentFromConfig(backendConfig string) (map[string]string, error) {
	source, err := artifacts.ReadBoundedUTF8File(
		backendConfig,
		referenceBackendReadBudget(),
		artifacts.StableReadOptions{FollowSymlinks: true},
	)
	if err != nil {
		if invalidReferenceBackendRead(err) {
			return nil, invalidReferenceBackendFailure(
				fmt.Sprintf(
					"cross-state backend config must be a UTF-8 regular JSON file no larger than %d bytes",
					maxReferenceBackendConfigBytes,
				),
			)
		}
		return nil, referenceBackendFailure(
			"REFERENCE_BACKEND_CONFIG_READ_FAILED",
			"unable to read cross-state backend config",
			procerr.CategoryIO,
		)
	}
	if source.Digest.Size <= 0 {
		return nil, invalidReferenceBackendFailure(fmt.Sprintf(
			"cross-state backend config must be between 1 and %d bytes",
			maxReferenceBackendConfigBytes,
		))
	}

	parsed, err := canonjson.ParseControlJSON(source.Text)
	if err != nil {
		return nil, invalidReferenceBackendFailure(
			"cross-state azurerm BACKEND_CONFIG must be a JSON object; HCL backend files remain supported when cross-state references are disabled",
		)
	}
	object, ok := parsed.(map[string]any)
	if !ok || len(object) == 0 {
		return nil, invalidReferenceBackendFailure(
			"cross-state backend config must contain a non-empty JSON object",
		)
	}

	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	keys = canonjson.SortedStrings(keys)
	config := make(map[string]any, len(keys))
	for _, key := range keys {
		stringField := isReferenceBackendStringField(key)
		booleanField := isReferenceBackendBooleanField(key)
		if !stringField && !booleanField {
			return nil, referenceBackendFailure(
				"UNSAFE_REFERENCE_BACKEND_CONFIG",
				"cross-state backend config contains an unsupported field; only reviewed non-secret AzureRM address and behavior fields are allowed, state keys are derived per root, and credentials must come from the environment",
				procerr.CategoryDomain,
			)
		}
		value := object[key]
		if stringField {
			text, valid := value.(string)
			if !valid || text == "" {
				return nil, invalidReferenceBackendFailure(fmt.Sprintf(
					"cross-state backend config field %q must be a non-empty string",
					key,
				))
			}
		}
		if booleanField {
			if _, valid := value.(bool); !valid {
				return nil, invalidReferenceBackendFailure(fmt.Sprintf(
					"cross-state backend config field %q must be a boolean",
					key,
				))
			}
		}
		config[key] = value
	}
	rawStrings, err := referenceBackendRawStringValues(source.Text, config)
	if err != nil {
		// ParseControlJSON has already validated the complete JSON grammar and
		// the loop above has narrowed this particular contract to a flat object
		// of strings and booleans. Reaching this branch therefore indicates a
		// disagreement in this domain-specific token projection, not a second
		// externally visible parser failure mode.
		return nil, invalidReferenceBackendFailure(
			"cross-state azurerm BACKEND_CONFIG must be a JSON object; HCL backend files remain supported when cross-state references are disabled",
		)
	}

	return map[string]string{
		ReferenceBackendEnvironment: renderReferenceBackendConfig(keys, config, rawStrings),
	}, nil
}

// referenceBackendRawStringValues walks only the already-validated, flat
// reference-backend object. It is deliberately not a general JSON parser: the
// strict control parser owns grammar, duplicate-key, depth, and number checks;
// this token pass retains the raw lexemes of the five allowed string fields so a
// lone UTF-16 surrogate is not collapsed by encoding/json to U+FFFD.
func referenceBackendRawStringValues(text string, config map[string]any) (map[string][]uint16, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, errors.New("reference-backend JSON value is not an object")
	}

	values := make(map[string][]uint16)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("reference-backend JSON key is not a string")
		}
		beforeValue := int(decoder.InputOffset())
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		if _, isString := config[key].(string); !isString {
			continue
		}
		start, err := referenceBackendValueStart(text, beforeValue)
		if err != nil {
			return nil, err
		}
		units, err := decodeReferenceBackendStringToken(text[start:int(decoder.InputOffset())])
		if err != nil {
			return nil, err
		}
		values[key] = units
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return values, nil
}

func referenceBackendValueStart(text string, offset int) (int, error) {
	for offset < len(text) {
		switch text[offset] {
		case ' ', '\t', '\n', '\r':
			offset++
		default:
			if text[offset] != ':' {
				return 0, errors.New("reference-backend JSON member has no value delimiter")
			}
			offset++
			for offset < len(text) {
				switch text[offset] {
				case ' ', '\t', '\n', '\r':
					offset++
				default:
					return offset, nil
				}
			}
		}
	}
	return 0, errors.New("reference-backend JSON member has no value")
}

func decodeReferenceBackendStringToken(token string) ([]uint16, error) {
	if len(token) < 2 || token[0] != '"' || token[len(token)-1] != '"' {
		return nil, errors.New("invalid reference-backend JSON string token")
	}
	units := make([]uint16, 0, len(token)-2)
	for index := 1; index < len(token)-1; {
		if token[index] != '\\' {
			character, size := utf8.DecodeRuneInString(token[index : len(token)-1])
			if character == utf8.RuneError && size == 1 {
				return nil, errors.New("invalid UTF-8 in reference-backend JSON string")
			}
			units = appendReferenceBackendRune(units, character)
			index += size
			continue
		}
		index++
		if index >= len(token)-1 {
			return nil, errors.New("incomplete reference-backend JSON escape")
		}
		switch token[index] {
		case '"', '\\', '/':
			units = append(units, uint16(token[index]))
			index++
		case 'b':
			units = append(units, '\b')
			index++
		case 'f':
			units = append(units, '\f')
			index++
		case 'n':
			units = append(units, '\n')
			index++
		case 'r':
			units = append(units, '\r')
			index++
		case 't':
			units = append(units, '\t')
			index++
		case 'u':
			if index+5 >= len(token) {
				return nil, errors.New("incomplete reference-backend Unicode escape")
			}
			decoded, err := strconv.ParseUint(token[index+1:index+5], 16, 16)
			if err != nil {
				return nil, errors.New("invalid reference-backend Unicode escape")
			}
			units = append(units, uint16(decoded))
			index += 5
		default:
			return nil, errors.New("invalid reference-backend JSON escape")
		}
	}
	return units, nil
}

func appendReferenceBackendRune(units []uint16, character rune) []uint16 {
	if character <= 0xffff {
		return append(units, uint16(character))
	}
	high, low := utf16.EncodeRune(character)
	return append(units, uint16(high), uint16(low))
}

func renderReferenceBackendConfig(keys []string, config map[string]any, rawStrings map[string][]uint16) string {
	var out strings.Builder
	out.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			out.WriteByte(',')
		}
		writeJSONString(&out, key)
		out.WriteByte(':')
		switch value := config[key].(type) {
		case string:
			writeJSONStringUnits(&out, rawStrings[key])
		case bool:
			if value {
				out.WriteString("true")
			} else {
				out.WriteString("false")
			}
		}
	}
	out.WriteByte('}')
	return out.String()
}

// writeJSONString renders the valid-UTF-8 string subset reachable after
// ParseControlJSON exactly as JSON.stringify does, including literal non-ASCII
// characters and U+2028/U+2029 rather than encoding/json's HTML-safe escapes.
func writeJSONString(out *strings.Builder, value string) {
	writeJSONStringUnits(out, utf16.Encode([]rune(value)))
}

func writeJSONStringUnits(out *strings.Builder, units []uint16) {
	const hex = "0123456789abcdef"
	out.WriteByte('"')
	for index := 0; index < len(units); index++ {
		unit := units[index]
		switch unit {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteByte(byte(unit))
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if unit < 0x20 {
				out.WriteString(`\u00`)
				out.WriteByte(hex[byte(unit)>>4])
				out.WriteByte(hex[byte(unit)&0x0f])
				continue
			}
			if unit >= 0xd800 && unit <= 0xdbff && index+1 < len(units) {
				low := units[index+1]
				if low >= 0xdc00 && low <= 0xdfff {
					out.WriteRune(utf16.DecodeRune(rune(unit), rune(low)))
					index++
					continue
				}
			}
			if unit >= 0xd800 && unit <= 0xdfff {
				out.WriteString(`\u`)
				out.WriteByte(hex[unit>>12])
				out.WriteByte(hex[(unit>>8)&0x0f])
				out.WriteByte(hex[(unit>>4)&0x0f])
				out.WriteByte(hex[unit&0x0f])
				continue
			}
			out.WriteRune(rune(unit))
		}
	}
	out.WriteByte('"')
}
