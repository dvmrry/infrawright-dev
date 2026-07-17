package roots

// scopeplan_helpers_test.go holds fixtures shared by scopepaths_test.go
// and planroots_test.go.

import "github.com/dvmrry/infrawright-dev/go/internal/procerr"

// asProcessFailure unwraps err into a *procerr.ProcessFailure, the type
// every panic this package's deploymentError/domainError/domainErrorCode/
// internalError helpers raise ultimately becomes once recoverProcessFailure
// converts it to a normal error return.
func asProcessFailure(err error) (*procerr.ProcessFailure, bool) {
	pf, ok := err.(*procerr.ProcessFailure)
	return pf, ok
}
