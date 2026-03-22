package launcher

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
)

func extractContainerlabBin(r io.Reader) error {
	gzipReader, err := gzip.NewReader(r)
	if err != nil {
		return err
	}

	defer func() {
		_ = gzipReader.Close()
	}()

	tarReader := tar.NewReader(gzipReader)

	f, err := os.OpenFile(
		"/usr/bin/containerlab",
		os.O_CREATE|os.O_RDWR,
		clabernetesconstants.PermissionsEveryoneAllPermissions,
	)
	if err != nil {
		return err
	}

	defer func() {
		_ = f.Close()
	}()

	for {
		var h *tar.Header

		h, err = tarReader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return err
		}

		if h.Name != "containerlab" {
			// not the clab bin, we don't care
			continue
		}

		_, err = io.Copy(f, tarReader) //nolint: gosec
		if err != nil {
			return err
		}

		return nil
	}
}

func (c *clabernetes) installContainerlabVersion(version string) error {
	dir, err := os.MkdirTemp("", "")
	if err != nil {
		return err
	}

	defer func() {
		_ = os.RemoveAll(dir)
	}()

	tarName := fmt.Sprintf("containerlab_%s_Linux_amd64.tar.gz", version)

	outTarFile, err := os.Create(fmt.Sprintf("%s/%s", dir, tarName))
	if err != nil {
		return err
	}

	err = clabernetesutil.WriteHTTPContentsFromPath(
		context.Background(),
		fmt.Sprintf(
			"https://github.com/srl-labs/containerlab/releases/download/v%s/%s",
			version,
			tarName,
		),
		outTarFile,
		nil,
	)
	if err != nil {
		return err
	}

	inTarFile, err := os.Open(fmt.Sprintf("%s/%s", dir, tarName))
	if err != nil {
		return err
	}

	return extractContainerlabBin(inTarFile)
}

// containerlabWorkdir is the working directory for containerlab in containerd mode.
// Lab files are written here so that the host containerd daemon can resolve bind-mount
// source paths (it looks up paths on the HOST filesystem, not the pod overlay).
// This path is backed by a hostPath volume so both the pod and the host see the same files.
const containerlabWorkdir = "/var/lib/clabernetes"

func (c *clabernetes) containerdMode() bool {
	return os.Getenv(clabernetesconstants.LauncherContainerRuntimeEnv) ==
		clabernetesconstants.LauncherContainerRuntimeContainerd
}

// containerlabCwd returns the working directory for containerlab commands.
// In containerd mode we use the shared host path; otherwise the default pod cwd.
func (c *clabernetes) containerlabCwd() string {
	if c.containerdMode() {
		return containerlabWorkdir
	}

	return ""
}

func (c *clabernetes) preDestroyContainerlab(outWriter io.Writer) {
	// Run `containerlab destroy` before deploy to clean up any stale interfaces or containers
	// left from a previously crashed pod. Errors are intentionally ignored -- if there is nothing
	// to destroy this will simply exit non-zero and we continue to deploy.
	destroyArgs := []string{
		"destroy",
		"-t",
		"topo.clab.yaml",
		"--cleanup",
	}

	destroyArgs = append(destroyArgs, c.runtime.ContainerlabArgs()...)

	destroyCmd := exec.CommandContext(c.ctx, "containerlab", destroyArgs...)
	destroyCmd.Dir = c.containerlabCwd()
	destroyCmd.Stdout = outWriter
	destroyCmd.Stderr = outWriter

	_ = destroyCmd.Run()

	// In containerd mode, the named network namespace at /run/netns/clab-<topology>-<node>
	// persists on the host between pod restarts via the Bidirectional /run/netns mount.
	// If the previous run created interfaces inside that netns before crashing, they will
	// still be there when the new pod starts. Delete the stale netns explicitly.
	// The containerlab lab name inside the topo.clab.yaml is the app name ("clabernetes"),
	// so named netns follow the pattern: clab-<appName>-<nodeName>.
	appName := clabernetesutil.GetEnvStrOrDefault(
		clabernetesconstants.AppNameEnv,
		clabernetesconstants.AppNameDefault,
	)
	nodeName := os.Getenv(clabernetesconstants.LauncherNodeNameEnv)

	if nodeName != "" {
		// The topology uses prefix:"" so containerlab names the container (and its
		// named netns) by just the short node name. Also try the legacy
		// "clab-<appName>-<nodeName>" form in case prefix has been changed.
		for _, nsName := range []string{
			nodeName,
			fmt.Sprintf("clab-%s-%s", appName, nodeName),
		} {
			cleanCmd := exec.Command("ip", "netns", "delete", nsName) //nolint:gosec
			cleanCmd.Stdout = outWriter
			cleanCmd.Stderr = outWriter
			_ = cleanCmd.Run()
		}

		// In containerd mode with hostNetwork:true, CNI creates veth interfaces directly in
		// the host network namespace. These persist between pod restarts. Delete any stale
		// link whose name starts with "<nodeName>-" (e.g. srl1-e1-1) since they belong to
		// this node's previous incarnation and will cause "already exists" errors on re-deploy.
		// `ip -o link show` outputs one interface per line: "<idx>: <ifname>: ..."
		linksOut, linksErr := exec.Command("ip", "-o", "link", "show").Output() //nolint:gosec
		if linksErr == nil {
			// Clean up stale CNI veth interfaces (e.g. srl1-e1-1) AND stale VXLAN
			// interfaces (e.g. vx-srl1-e1-1) left from a previous pod incarnation.
			vxPrefix := "vx-" + nodeName + "-"
			cniPrefix := nodeName + "-"
			for _, line := range strings.Split(string(linksOut), "\n") {
				// Each line: "2: eth0: <flags> ..."
				fields := strings.Fields(line)
				if len(fields) < 2 {
					continue
				}
				rawName := strings.TrimSuffix(fields[1], ":")
				// Strip "@peer" suffix (e.g. "srl2-e1-1@clab-ab1d98e5" → "srl2-e1-1")
				ifName := strings.SplitN(rawName, "@", 2)[0]
				if strings.HasPrefix(ifName, cniPrefix) || strings.HasPrefix(ifName, vxPrefix) {
					delCmd := exec.Command("ip", "link", "delete", ifName) //nolint:gosec
					delCmd.Stdout = outWriter
					delCmd.Stderr = outWriter
					_ = delCmd.Run()
				}
			}
		}
	}
}

func (c *clabernetes) runContainerlab() error {
	containerlabLogFile, err := os.Create("containerlab.log")
	if err != nil {
		return err
	}

	containerlabOutWriter := io.MultiWriter(c.containerlabLogger, containerlabLogFile)

	c.preDestroyContainerlab(containerlabOutWriter)

	// In containerd mode, copy topo.clab.yaml to the shared workdir so that containerlab
	// can find it and writes the lab directory at a host-accessible path.
	if c.containerdMode() {
		if err = os.MkdirAll(containerlabWorkdir, 0o755); err != nil { //nolint:gosec
			return fmt.Errorf("failed creating containerd workdir: %w", err)
		}

		src, err := os.ReadFile("topo.clab.yaml")
		if err != nil {
			return fmt.Errorf("failed reading topo.clab.yaml: %w", err)
		}

		if err = os.WriteFile( //nolint:gosec
			containerlabWorkdir+"/topo.clab.yaml", src, 0o644,
		); err != nil {
			return fmt.Errorf("failed copying topo.clab.yaml to workdir: %w", err)
		}
	}

	args := []string{
		"deploy",
		"-t",
		"topo.clab.yaml",
	}

	if !(os.Getenv(clabernetesconstants.LauncherContainerlabPersist) == clabernetesconstants.True) {
		args = append(args, "--reconfigure")
	}

	if os.Getenv(clabernetesconstants.LauncherContainerlabDebug) == clabernetesconstants.True {
		args = append(args, "--debug")
	}

	containerlabTimeout := os.Getenv(clabernetesconstants.LauncherContainerlabTimeout)
	if containerlabTimeout != "" {
		args = append(args, []string{"--timeout", containerlabTimeout}...)
	}

	args = append(args, c.runtime.ContainerlabArgs()...)

	if extraArgsEnv := os.Getenv(clabernetesconstants.LauncherContainerlabExtraArgs); extraArgsEnv != "" {
		args = append(args, strings.Fields(extraArgsEnv)...)
	}

	cmd := exec.CommandContext(c.ctx, "containerlab", args...)
	cmd.Dir = c.containerlabCwd()
	cmd.Stdout = containerlabOutWriter
	cmd.Stderr = containerlabOutWriter

	err = cmd.Run()
	if err != nil {
		return err
	}

	return nil
}
