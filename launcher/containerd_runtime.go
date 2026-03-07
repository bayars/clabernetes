package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
)

const (
	containerdDefaultNamespace = "clab"
)

// containerdRuntime implements the containerRuntime interface by talking directly to the host's
// containerd socket via nerdctl. No Docker daemon is started -- containerlab uses the containerd
// runtime directly.
type containerdRuntime struct {
	socketPath string
	namespace  string
}

func newContainerdRuntime() *containerdRuntime {
	return &containerdRuntime{
		socketPath: fmt.Sprintf(
			"%s/%s",
			clabernetesconstants.LauncherCRISockPath,
			clabernetesconstants.KubernetesCRISockContainerd,
		),
		namespace: containerdDefaultNamespace,
	}
}

func (c *containerdRuntime) nerdctlBase() []string {
	return []string{
		"--address", c.socketPath,
		"--namespace", c.namespace,
	}
}

func (c *containerdRuntime) Setup(_ context.Context, logger io.Writer) error {
	// no daemon to start -- just verify the socket is accessible
	_, err := os.Stat(c.socketPath)
	if err != nil {
		return fmt.Errorf(
			"containerd socket not found at %s: %w", c.socketPath, err,
		)
	}

	fmt.Fprintf(logger, "containerd runtime: socket verified at %s\n", c.socketPath)

	return nil
}

func (c *containerdRuntime) GetContainerIDs(
	ctx context.Context,
	all bool,
) ([]string, error) {
	args := c.nerdctlBase()
	args = append(args, "ps")

	if all {
		args = append(args, "-a")
	}

	args = append(args, "--quiet")

	psCmd := exec.CommandContext(ctx, "nerdctl", args...)

	output, err := psCmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(output), "\n")

	var containerIDs []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			containerIDs = append(containerIDs, trimmed)
		}
	}

	return containerIDs, nil
}

func (c *containerdRuntime) GetContainerIDForNodeName(
	ctx context.Context,
	nodeName string,
) (string, error) {
	args := c.nerdctlBase()
	args = append(args,
		"ps",
		"--quiet",
		"--filter",
		fmt.Sprintf("name=%s", nodeName),
	)

	psCmd := exec.CommandContext(ctx, "nerdctl", args...) //nolint:gosec

	output, err := psCmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

func (c *containerdRuntime) GetContainerAddr(
	ctx context.Context,
	containerID string,
) (string, error) {
	args := c.nerdctlBase()
	args = append(args,
		"inspect",
		"--format", "json",
		containerID,
	)

	inspectCmd := exec.CommandContext(ctx, "nerdctl", args...)

	output, err := inspectCmd.Output()
	if err != nil {
		return "", err
	}

	// nerdctl inspect returns a JSON array
	var inspectResults []struct {
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}

	if err = json.Unmarshal(output, &inspectResults); err != nil {
		return "", fmt.Errorf("failed parsing nerdctl inspect output: %w", err)
	}

	if len(inspectResults) == 0 {
		return "", fmt.Errorf("no inspect results for container %s", containerID)
	}

	for _, network := range inspectResults[0].NetworkSettings.Networks {
		if network.IPAddress != "" {
			return network.IPAddress, nil
		}
	}

	return "", fmt.Errorf("no IP address found for container %s", containerID)
}

func (c *containerdRuntime) PrintContainerLogs(
	ctx context.Context,
	logger claberneteslogging.Instance,
	containerIDs []string,
) {
	for _, containerID := range containerIDs {
		args := c.nerdctlBase()
		args = append(args, "logs", containerID)

		cmd := exec.CommandContext(ctx, "nerdctl", args...) //nolint:gosec

		cmd.Stdout = logger
		cmd.Stderr = logger

		err := cmd.Run()
		if err != nil {
			logger.Warnf(
				"printing node logs for container id %q failed, err: %s", containerID, err,
			)
		}
	}
}

func (c *containerdRuntime) TailContainerLogs(
	ctx context.Context,
	logger claberneteslogging.Instance,
	nodeLogger io.Writer,
	containerIDs []string,
) error {
	nodeLogFile, err := os.Create("node.log")
	if err != nil {
		return err
	}

	nodeOutWriter := io.MultiWriter(nodeLogger, nodeLogFile)

	for _, containerID := range containerIDs {
		go func(containerID string, nodeOutWriter io.Writer) {
			args := c.nerdctlBase()
			args = append(args, "logs", "-f", containerID)

			cmd := exec.CommandContext(ctx, "nerdctl", args...) //nolint:gosec

			cmd.Stdout = nodeOutWriter
			cmd.Stderr = nodeOutWriter

			err = cmd.Run()
			if err != nil {
				logger.Warnf(
					"tailing node logs for container id %q failed, err: %s", containerID, err,
				)
			}
		}(containerID, nodeOutWriter)
	}

	return nil
}

func (c *containerdRuntime) Cleanup(_ context.Context, _ io.Writer) {
	// no-op for containerd -- images are on the host, no DinD to prune
}

func (c *containerdRuntime) ContainerlabArgs() []string {
	return []string{
		"--runtime", "containerd",
		"--containerd-socket", c.socketPath,
	}
}

func (c *containerdRuntime) NeedsImagePullThrough() bool {
	return false
}
