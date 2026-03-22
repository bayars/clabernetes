package image

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	claberneteslogging "github.com/srl-labs/clabernetes/logging"
)

type containerdManager struct {
	logger claberneteslogging.Instance
}

func (m *containerdManager) Present(ctx context.Context, imageName string) (bool, error) {
	checkCmd := exec.CommandContext( //nolint:gosec
		ctx,
		"nerdctl",
		"--address",
		"/run/containerd/containerd.sock",
		"--namespace",
		"k8s.io",
		"image",
		"list",
		"--filter",
		fmt.Sprintf("reference=%s", imageName),
		"--quiet",
	)

	output, err := checkCmd.Output()
	if err != nil {
		return false, err
	}

	if len(output) == 0 {
		return false, nil
	}

	return true, nil
}

func (m *containerdManager) Export(ctx context.Context, imageName, destination string) error {
	// Use a cancellable child context so we can abort nerdctl save early if needed.
	saveCtx, cancelSave := context.WithCancel(ctx)
	defer cancelSave()

	exportCmd := exec.CommandContext(
		saveCtx,
		"nerdctl",
		"--address",
		"/run/containerd/containerd.sock",
		"--namespace",
		"k8s.io",
		"image",
		"save",
		"--output",
		destination,
		imageName,
	)

	exportCmd.Stdout = m.logger
	exportCmd.Stderr = m.logger

	if err := exportCmd.Start(); err != nil {
		return fmt.Errorf("image save failed to start: %w", err)
	}

	cmdDone := make(chan error, 1)
	go func() { cmdDone <- exportCmd.Wait() }()

	// nerdctl save writes TAR data immediately when blobs are in the content store.
	// When containerd has discard_unpacked_layers=true (GKE default), the layer blobs
	// are gone and nerdctl save silently tries to re-fetch them from the registry.
	// That resolve attempt runs in the pod's network namespace, which has no access to
	// external registries, causing a ~30s TCP connection timeout before failing.
	//
	// Detect this by checking whether any bytes have been written to the destination
	// file after progressTimeout. If none, abort immediately so the caller can fall
	// back to Docker pull ("auto" mode) rather than blocking for 30s.
	progressTimer := time.NewTimer(5 * time.Second)
	defer progressTimer.Stop()

	select {
	case err := <-cmdDone:
		if err != nil {
			return fmt.Errorf("image save failed: %w", err)
		}
	case <-progressTimer.C:
		fi, statErr := os.Stat(destination)
		if statErr != nil || fi.Size() == 0 {
			// No output written -- save is stuck resolving missing blobs from the
			// registry. Abort early instead of waiting for the TCP timeout.
			cancelSave()
			<-cmdDone // drain

			return fmt.Errorf(
				"image %q save stalled with no output after 5s -- blobs are likely "+
					"unavailable (containerd discard_unpacked_layers=true); "+
					"set imagePullThroughMode: auto to fall back to Docker pull",
				imageName,
			)
		}

		// File has content -- save is actively writing, wait for it to finish.
		if err := <-cmdDone; err != nil {
			return fmt.Errorf("image save failed: %w", err)
		}
	}

	m.logger.Debugf("image %q exported from containerd successfully...", imageName)

	return nil
}
