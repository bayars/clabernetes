//go:build linux

package launcher

import (
	"fmt"
	"os"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslauncherconnectivity "github.com/srl-labs/clabernetes/launcher/connectivity"
)

// mgmtBridge establishes the management bridge VXLAN segment after containerlab has created the
// local management bridge. It connects this pod's containerlab management bridge to all other
// launcher pods' management bridges via VXLAN, making every mgmt-ipv4 address reachable at L2.
func (c *clabernetes) mgmtBridge() {
	allNodes, err := c.getAllNodes()
	if err != nil {
		c.logger.Fatalf("failed fetching all-nodes from connectivity CR, err: %s", err)
	}

	nodeName := os.Getenv(clabernetesconstants.LauncherNodeNameEnv)

	// The containerlab management bridge name follows containerlab's convention: clab-<topology-name>.
	// The sub-topology name is "clabernetes-<nodeName>" as set by the controller.
	localMgmtBridge := fmt.Sprintf("clab-clabernetes-%s", nodeName)

	claberneteslauncherconnectivity.SetupMgmtBridge(
		c.ctx,
		localMgmtBridge,
		nodeName,
		allNodes,
		c.logger,
	)
}
