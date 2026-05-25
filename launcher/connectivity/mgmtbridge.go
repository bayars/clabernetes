//go:build linux

package connectivity

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
)

const (
	mgmtBridgeName  = "clab-mgmt-br"
	mgmtVxIfPrefix  = "mgmt-vx-"
)

// SetupMgmtBridge creates a shared management-bridge VXLAN segment that joins this launcher pod's
// containerlab management bridge with every other node's management bridge across the cluster.
// After this call all mgmt-ipv4 addresses in the topology are reachable at L2 from this pod.
//
//   - localMgmtBridge: name of the containerlab management bridge on this pod (e.g. clab-clabernetes-srl1)
//   - localNodeName: this pod's node name (used to skip self from allNodes)
//   - allNodes: map of nodeName → vx service DNS from ConnectivitySpec.AllNodes
//   - logger: logger instance
func SetupMgmtBridge(
	ctx context.Context,
	localMgmtBridge,
	localNodeName string,
	allNodes map[string]string,
	logger claberneteslogging.Instance,
) {
	logger.Info("setting up management bridge VXLAN segment...")

	vxIfaces := make([]string, 0, len(allNodes))

	for nodeName, vxDNS := range allNodes {
		if nodeName == localNodeName {
			continue
		}

		ifName := mgmtVxIfPrefix + nodeName

		if err := createMgmtVxlan(ctx, ifName, vxDNS, logger); err != nil {
			logger.Fatalf(
				"failed creating mgmt vxlan interface %q to %q: %s",
				ifName, vxDNS, err,
			)
		}

		vxIfaces = append(vxIfaces, ifName)
	}

	if err := buildMgmtLinuxBridge(ctx, localMgmtBridge, vxIfaces, logger); err != nil {
		logger.Fatalf("failed building management linux bridge: %s", err)
	}

	logger.Info("management bridge VXLAN segment established")
}

func createMgmtVxlan(
	ctx context.Context,
	ifName,
	vxRemote string,
	logger claberneteslogging.Instance,
) error {
	// resolve the remote IP with the same retry logic as point-to-point tunnels
	m := &vxlanManager{common: &common{ctx: ctx, logger: logger}}

	resolvedIP, err := m.resolveVXLANService(vxRemote)
	if err != nil {
		return err
	}

	// delete any pre-existing interface with this name (best-effort)
	delCmd := exec.CommandContext(ctx, "ip", "link", "delete", ifName) //nolint:gosec
	_ = delCmd.Run()

	cmd := exec.CommandContext( //nolint:gosec
		ctx,
		"containerlab",
		"tools",
		"vxlan",
		"create",
		"--remote", resolvedIP,
		"--id", strconv.Itoa(clabernetesconstants.MgmtBridgeVNID),
		"--link", ifName,
		"--port", strconv.Itoa(clabernetesconstants.VXLANServicePort),
	)

	logger.Debugf("creating mgmt vxlan interface: %v", cmd.Args)

	cmd.Stdout = logger
	cmd.Stderr = logger

	return cmd.Run()
}

func buildMgmtLinuxBridge(
	ctx context.Context,
	localMgmtBridge string,
	vxIfaces []string,
	logger claberneteslogging.Instance,
) error {
	// create the linux bridge
	if err := runIP(ctx, logger, "link", "add", "name", mgmtBridgeName, "type", "bridge"); err != nil {
		return fmt.Errorf("add bridge: %w", err)
	}

	// enslave the local containerlab management bridge
	if err := runIP(ctx, logger, "link", "set", localMgmtBridge, "master", mgmtBridgeName); err != nil {
		return fmt.Errorf("set %s master: %w", localMgmtBridge, err)
	}

	// enslave each mgmt vxlan interface
	for _, iface := range vxIfaces {
		if err := runIP(ctx, logger, "link", "set", iface, "master", mgmtBridgeName); err != nil {
			return fmt.Errorf("set %s master: %w", iface, err)
		}
	}

	// bring the bridge up
	if err := runIP(ctx, logger, "link", "set", mgmtBridgeName, "up"); err != nil {
		return fmt.Errorf("set bridge up: %w", err)
	}

	return nil
}

func runIP(ctx context.Context, logger claberneteslogging.Instance, args ...string) error {
	cmd := exec.CommandContext(ctx, "ip", args...) //nolint:gosec

	logger.Debugf("running: ip %v", args)

	cmd.Stdout = logger
	cmd.Stderr = logger

	return cmd.Run()
}
