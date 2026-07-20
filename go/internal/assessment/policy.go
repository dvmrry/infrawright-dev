package assessment

import (
	"errors"
	"path/filepath"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// BoundDriftPolicy binds parsed policy meaning to the exact stable source
// bytes that produced it. Nil Path and File values represent no policy.
type BoundDriftPolicy struct {
	Path   *string
	File   *artifacts.StableFileDigest
	Policy *metadata.DriftPolicy
}

// DriftPolicyLoadFailure reports invalid policy content while retaining the
// stable source digest needed by assessment evidence. The source bytes and
// path are never included in its public failure text.
type DriftPolicyLoadFailure struct {
	*procerr.ProcessFailure
	File artifacts.StableFileDigest
}

// Unwrap exposes the shared ProcessFailure spine for errors.As callers.
func (f *DriftPolicyLoadFailure) Unwrap() error {
	return f.ProcessFailure
}

func assessmentDomainFailure(code, message string) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: procerr.CategoryDomain,
		Message:  message,
	})
}

func invalidDriftPolicyFailure(
	file artifacts.StableFileDigest,
	message string,
) *DriftPolicyLoadFailure {
	return &DriftPolicyLoadFailure{
		ProcessFailure: assessmentDomainFailure("INVALID_DRIFT_POLICY", message),
		File:           file,
	}
}

// LoadBoundDriftPolicy parses a stable, no-follow policy file under the
// control-JSON dialect and binds its digest. A nil path selects the empty
// version-1 policy and performs no filesystem read.
func LoadBoundDriftPolicy(
	policyPath *string,
	budget *artifacts.ReadBudget,
) (BoundDriftPolicy, error) {
	if policyPath == nil {
		policy, err := metadata.NewDriftPolicy(nil, "<policy>")
		if err != nil {
			return BoundDriftPolicy{}, invalidDriftPolicyFailure(
				artifacts.StableFileDigest{},
				"saved-plan drift policy is invalid",
			)
		}
		return BoundDriftPolicy{Policy: policy}, nil
	}
	if !filepath.IsAbs(*policyPath) {
		return BoundDriftPolicy{}, assessmentDomainFailure(
			"UNRESOLVED_POLICY_PATH",
			"saved-plan policy requires a resolved absolute path",
		)
	}
	source, err := artifacts.ReadBoundedFileBytes(
		*policyPath,
		budget,
		artifacts.StableReadOptions{},
	)
	if err != nil {
		return BoundDriftPolicy{}, err
	}
	defer clear(source.Bytes)

	message := "saved-plan drift policy is invalid"
	if !utf8.Valid(source.Bytes) {
		return BoundDriftPolicy{}, invalidDriftPolicyFailure(source.Digest, message)
	}
	value, err := canonjson.ParseControlJSON(string(source.Bytes))
	if err != nil {
		var decodeFailure *canonjson.PythonJSONDecodeError
		if errors.As(err, &decodeFailure) {
			message = decodeFailure.Error()
		}
		return BoundDriftPolicy{}, invalidDriftPolicyFailure(source.Digest, message)
	}
	policy, err := metadata.NewDriftPolicy(value, "<policy>")
	if err != nil {
		return BoundDriftPolicy{}, invalidDriftPolicyFailure(source.Digest, message)
	}
	pathCopy := *policyPath
	digestCopy := source.Digest
	return BoundDriftPolicy{
		Path:   &pathCopy,
		File:   &digestCopy,
		Policy: policy,
	}, nil
}

// RecheckBoundDriftPolicy verifies that a bound policy path still contains
// the same stable bytes. Same-byte replacement is accepted, matching the
// source digest-and-size binding.
func RecheckBoundDriftPolicy(
	bound BoundDriftPolicy,
	budget *artifacts.ReadBudget,
) error {
	if bound.Path == nil || bound.File == nil {
		return nil
	}
	current, err := artifacts.SHA256StableFile(
		*bound.Path,
		budget,
		artifacts.StableReadOptions{},
	)
	if err != nil {
		return err
	}
	if current != *bound.File {
		return assessmentDomainFailure(
			"DRIFT_POLICY_CHANGED",
			"saved-plan drift policy changed during assessment",
		)
	}
	return nil
}
