//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package terraformcmd

import (
	"errors"
	"os"
)

func inheritedPlanFilePath() (string, error) {
	return "", errors.New("inherited plan descriptors are unsupported")
}

func validateInheritedPlanFile(*os.File) error {
	return errors.New("inherited plan descriptors are unsupported")
}
