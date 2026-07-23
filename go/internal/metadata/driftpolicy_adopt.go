package metadata

// IsSupportedDriftPolicyVersion ports isSupportedDriftPolicyVersion from
// node-src/domain/drift-policy.ts. Block D uses it while layering user policy
// entries over active-pack policy without rounding lossless JSON numbers.
func IsSupportedDriftPolicyVersion(value any) bool {
	return isDriftPolicyVersionOne(value)
}
