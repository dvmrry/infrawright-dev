package deployment

import "github.com/dvmrry/infrawright-dev/go/internal/controlevidence"

// BoundAssessmentDeployment pairs a validated deployment with the stable
// control-file evidence for the exact source bytes that produced it. A missing
// optional deployment path yields the normal default deployment and an absent
// file binding.
type BoundAssessmentDeployment struct {
	Deployment Deployment
	File       controlevidence.BoundAssessmentControlFile
}

// LoadBoundAssessmentDeployment ports loadBoundAssessmentDeployment from
// the original implementation. It loads the selected optional deployment
// through the assessment control-evidence reader before parsing that bound text,
// so later assessment rechecks cover the same bytes and filesystem identity.
func LoadBoundAssessmentDeployment(
	deploymentPath string,
	options controlevidence.BindOptions,
) (bound BoundAssessmentDeployment, err error) {
	defer recoverProcessFailure(&err)

	source, err := controlevidence.BindOptionalAssessmentControlText(
		deploymentPath,
		options,
	)
	if err != nil {
		return BoundAssessmentDeployment{}, err
	}
	return BoundAssessmentDeployment{
		Deployment: deploymentFromText(source.Text),
		File:       source.File,
	}, nil
}
