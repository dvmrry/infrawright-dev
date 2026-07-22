package metadata

// IsSupportedDriftPolicyVersion ports isSupportedDriftPolicyVersion from
// the original implementation. Block D uses it while layering user policy
// entries over active-pack policy without rounding lossless JSON numbers.
func IsSupportedDriftPolicyVersion(value any) bool {
	return isDriftPolicyVersionOne(value)
}
