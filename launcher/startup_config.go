package launcher

import (
	"io"
	"os"
	"path/filepath"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
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

	seedPath := filepath.Join(
		clabernetesconstants.StartupConfigSeedMountPath,
		clabernetesconstants.StartupConfigFileName,
	)

	// Check if the seed mount exists at all — if not, this node has no startup config.
	if _, err := os.Stat(seedPath); os.IsNotExist(err) {
		return nil
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
