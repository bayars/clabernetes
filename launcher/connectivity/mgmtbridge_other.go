//go:build !linux

package connectivity

import (
	"context"

	claberneteslogging "github.com/srl-labs/clabernetes/logging"
)

// SetupMgmtBridge is a no-op on non-linux platforms.
func SetupMgmtBridge(
	_ context.Context,
	_,
	_ string,
	_ map[string]string,
	_ claberneteslogging.Instance,
) {
}
