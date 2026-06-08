package launcher

import (
	"io"
	"os"
	"path/filepath"
	"time"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
)

const (
	seedFileWaitInterval = 2 * time.Second
	seedFileWaitTimeout  = 30 * time.Second
)

// EnsureStartupConfig checks whether the startup-config PVC already has content. If not, and a
// seed file is present, it copies the seed content into the PVC directory. This is called once
// during launcher startup, before containerlab is invoked.
//
// On first launch the PVC is empty → seed is copied.
// On subsequent launches (after a snapshot has updated the PVC) the file already exists → no-op.
func (c *clabernetes) ensureStartupConfig() error {
	pvcPath := filepath.Join(
		clabernetesconstants.StartupConfigPVCMountPath,
		clabernetesconstants.StartupConfigFileName,
	)

	seedDir := clabernetesconstants.StartupConfigSeedMountPath
	seedPath := filepath.Join(seedDir, clabernetesconstants.StartupConfigFileName)

	// If the seed mount directory doesn't exist, this node has no startup config.
	if _, err := os.Stat(seedDir); os.IsNotExist(err) {
		return nil
	}

	// The seed directory is mounted but the kubelet may not have synced the ConfigMap files yet
	// (common on slower cloud nodes). Wait up to seedFileWaitTimeout for the file to appear.
	if err := c.waitForSeedFile(seedPath); err != nil {
		return err
	}

	// PVC already has content (e.g. updated by a snapshot) — leave it untouched.
	if _, err := os.Stat(pvcPath); err == nil {
		c.logger.Debug("startup-config already present in PVC, skipping seed copy")

		return nil
	}

	c.logger.Infof("seeding startup-config from %q to %q", seedPath, pvcPath)

	if err := os.MkdirAll(filepath.Dir(pvcPath), clabernetesconstants.PermissionsEveryoneAllPermissions); err != nil {
		return err
	}

	return copyFile(seedPath, pvcPath)
}

// waitForSeedFile waits until the seed startup-config file appears in the mounted ConfigMap
// directory, retrying up to seedFileWaitTimeout. This handles the race between the container
// starting and the kubelet finishing its ConfigMap volume sync (observed on GKE).
func (c *clabernetes) waitForSeedFile(seedPath string) error {
	deadline := time.Now().Add(seedFileWaitTimeout)

	for {
		if _, err := os.Stat(seedPath); err == nil {
			return nil
		}

		if time.Now().After(deadline) {
			c.logger.Warnf(
				"seed startup-config file %q not found after %s, assuming no startup config",
				seedPath,
				seedFileWaitTimeout,
			)

			return nil
		}

		c.logger.Debugf(
			"waiting for seed startup-config file %q to appear (kubelet sync pending)...",
			seedPath,
		)

		time.Sleep(seedFileWaitInterval)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return err
	}

	defer func() {
		_ = in.Close()
	}()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	defer func() {
		_ = out.Close()
	}()

	_, err = io.Copy(out, in)

	return err
}
