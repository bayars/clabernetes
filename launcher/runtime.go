package launcher

import (
	"context"
	"io"

	claberneteslogging "github.com/srl-labs/clabernetes/logging"
)

// containerRuntime defines the interface for container runtimes used by the launcher. The launcher
// supports two runtimes: "docker" (DinD, legacy default) and "containerd" (direct host containerd
// socket access).
type containerRuntime interface {
	// Setup performs any runtime-specific setup (e.g. starting Docker daemon). The logger is used
	// for writing setup output.
	Setup(ctx context.Context, logger io.Writer) error
	// GetContainerIDs returns a list of container IDs managed by this runtime. When all is true,
	// includes stopped containers.
	GetContainerIDs(ctx context.Context, all bool) ([]string, error)
	// GetContainerIDForNodeName returns the container ID for the given node name.
	GetContainerIDForNodeName(ctx context.Context, nodeName string) (string, error)
	// GetContainerAddr returns the IP address of the container with the given ID.
	GetContainerAddr(ctx context.Context, containerID string) (string, error)
	// PrintContainerLogs prints logs for the given container IDs to the logger.
	PrintContainerLogs(ctx context.Context, logger claberneteslogging.Instance, ids []string)
	// TailContainerLogs tails (follows) logs for the given container IDs, writing output to
	// nodeLogger.
	TailContainerLogs(
		ctx context.Context,
		logger claberneteslogging.Instance,
		nodeLogger io.Writer,
		ids []string,
	) error
	// Cleanup performs any runtime-specific cleanup (e.g. docker system prune).
	Cleanup(ctx context.Context, logger io.Writer)
	// ContainerlabArgs returns extra arguments to pass to the containerlab deploy command for
	// this runtime (e.g. --runtime containerd).
	ContainerlabArgs() []string
	// NeedsImagePullThrough returns true if this runtime needs the image export/import flow
	// (Docker mode needs images copied from host CRI to DinD; containerd mode does not).
	NeedsImagePullThrough() bool
}
