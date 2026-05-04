package clabverter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	k8scorev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"
	"gopkg.in/yaml.v3"
)

const snapshotKeySeparator = "__"

// Unclabverter holds data and methods for the reverse conversion: from a clabverter output
// directory (or snapshot ConfigMap) back to a containerlab topology YAML and device config files
// organized as <NodeName>/<FileName>.
type Unclabverter struct {
	logger           claberneteslogging.Instance
	inputDirectory   string
	outputDirectory  string
	fromSnapshotFile string
}

// MustNewUnclabverter returns an instance of Unclabverter or panics.
func MustNewUnclabverter(
	inputDirectory,
	outputDirectory,
	fromSnapshotFile string,
	debug,
	quiet bool,
) *Unclabverter {
	logLevel := clabernetesconstants.Info

	if debug {
		logLevel = clabernetesconstants.Debug
	}

	if quiet {
		logLevel = clabernetesconstants.Disabled
	}

	claberneteslogging.InitManager(
		claberneteslogging.WithLogger(claberneteslogging.StdErrLog),
	)

	logManager := claberneteslogging.GetManager()

	oldLogger, _ := logManager.GetLogger(clabernetesconstants.Clabverter)
	if oldLogger != nil {
		logManager.DeleteLogger(clabernetesconstants.Clabverter)
	}

	logger := logManager.MustRegisterAndGetLogger(
		clabernetesconstants.Clabverter,
		logLevel,
	)

	return &Unclabverter{
		logger:           logger,
		inputDirectory:   inputDirectory,
		outputDirectory:  outputDirectory,
		fromSnapshotFile: fromSnapshotFile,
	}
}

// Unclabvert performs the reverse conversion.
func (u *Unclabverter) Unclabvert() error {
	u.logger.Info("starting reverse clabversion!")

	var err error

	u.outputDirectory, err = filepath.Abs(u.outputDirectory)
	if err != nil {
		return fmt.Errorf("failed resolving output directory: %w", err)
	}

	err = os.MkdirAll(u.outputDirectory, clabernetesconstants.PermissionsEveryoneReadWriteOwnerExecute)
	if err != nil {
		return fmt.Errorf("failed creating output directory: %w", err)
	}

	// Load Topology CR and ConfigMaps from input directory (if provided).
	var topologyCR *StatuslessTopology

	configMaps := map[string]k8scorev1.ConfigMap{}

	if u.inputDirectory != "" {
		topologyCR, configMaps, err = u.scanInputDirectory()
		if err != nil {
			return err
		}
	}

	// Determine config source: snapshot file or output-directory ConfigMaps.
	if u.fromSnapshotFile != "" {
		return u.unclabvertFromSnapshot(topologyCR)
	}

	if topologyCR == nil {
		return fmt.Errorf(
			"no Topology CR found in input directory %q; " +
				"provide --input-directory with a clabverter output directory " +
				"or use --from-snapshot",
			u.inputDirectory,
		)
	}

	return u.unclabvertFromOutputDir(topologyCR, configMaps)
}

// scanInputDirectory reads all *.yaml files in inputDirectory and classifies them as either the
// Topology CR or ConfigMaps.
func (u *Unclabverter) scanInputDirectory() (
	*StatuslessTopology,
	map[string]k8scorev1.ConfigMap,
	error,
) {
	absInput, err := filepath.Abs(u.inputDirectory)
	if err != nil {
		return nil, nil, fmt.Errorf("failed resolving input directory: %w", err)
	}

	entries, err := os.ReadDir(absInput)
	if err != nil {
		if os.IsNotExist(err) {
			// Directory doesn't exist — treat as empty (no Topology CR, no ConfigMaps).
			return nil, map[string]k8scorev1.ConfigMap{}, nil
		}

		return nil, nil, fmt.Errorf("failed reading input directory %q: %w", absInput, err)
	}

	var topologyCR *StatuslessTopology

	configMaps := map[string]k8scorev1.ConfigMap{}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		filePath := filepath.Join(absInput, name)

		data, readErr := os.ReadFile(filePath) //nolint:gosec
		if readErr != nil {
			u.logger.Warnf("skipping %s: %s", filePath, readErr)

			continue
		}

		// Peek at the kind field.
		var meta struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}

		if unmarshalErr := sigsyaml.Unmarshal(data, &meta); unmarshalErr != nil {
			u.logger.Debugf("skipping %s (not valid YAML): %s", name, unmarshalErr)

			continue
		}

		switch meta.Kind {
		case "Topology":
			var topo StatuslessTopology

			if unmarshalErr := sigsyaml.Unmarshal(data, &topo); unmarshalErr != nil {
				u.logger.Warnf("failed parsing Topology CR %s: %s", name, unmarshalErr)

				continue
			}

			topologyCR = &topo

			u.logger.Debugf("found Topology CR: %s", meta.Metadata.Name)

		case "ConfigMap":
			var cm k8scorev1.ConfigMap

			if unmarshalErr := sigsyaml.Unmarshal(data, &cm); unmarshalErr != nil {
				u.logger.Warnf("failed parsing ConfigMap %s: %s", name, unmarshalErr)

				continue
			}

			configMaps[cm.Name] = cm

			u.logger.Debugf("found ConfigMap: %s", cm.Name)
		}
	}

	return topologyCR, configMaps, nil
}

// unclabvertFromOutputDir reconstructs the containerlab YAML and device config files from the
// clabverter output directory's Topology CR and ConfigMaps.
func (u *Unclabverter) unclabvertFromOutputDir(
	topologyCR *StatuslessTopology,
	configMaps map[string]k8scorev1.ConfigMap,
) error {
	clabConfig, err := clabernetesutilcontainerlab.LoadContainerlabConfig(
		topologyCR.Spec.Definition.Containerlab,
	)
	if err != nil {
		return fmt.Errorf("failed parsing embedded containerlab definition: %w", err)
	}

	filesFromCM := topologyCR.Spec.Deployment.FilesFromConfigMap

	for nodeName, entries := range filesFromCM {
		for _, entry := range entries {
			cm, ok := configMaps[entry.ConfigMapName]
			if !ok {
				u.logger.Warnf(
					"ConfigMap %q not found in input directory (referenced by node %s), skipping",
					entry.ConfigMapName, nodeName,
				)

				continue
			}

			content, ok := cm.Data[entry.ConfigMapPath]
			if !ok {
				u.logger.Warnf(
					"key %q not found in ConfigMap %q, skipping",
					entry.ConfigMapPath, entry.ConfigMapName,
				)

				continue
			}

			outPath, writeErr := u.writeDeviceFile(nodeName, filepath.Base(entry.FilePath), content)
			if writeErr != nil {
				return writeErr
			}

			// Update startup-config reference if this entry came from a startup-config ConfigMap.
			if strings.HasSuffix(entry.ConfigMapName, "-startup-config") {
				if node, nodeOk := clabConfig.Topology.Nodes[nodeName]; nodeOk {
					relPath, relErr := filepath.Rel(u.outputDirectory, outPath)
					if relErr != nil {
						relPath = outPath
					}

					node.StartupConfig = relPath
				}
			}
		}
	}

	return u.writeClabYAML(clabConfig)
}

// unclabvertFromSnapshot extracts device config files from the snapshot ConfigMap YAML and
// optionally reconstructs the containerlab YAML if a Topology CR is available.
func (u *Unclabverter) unclabvertFromSnapshot(topologyCR *StatuslessTopology) error {
	data, err := os.ReadFile(u.fromSnapshotFile) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed reading snapshot file %q: %w", u.fromSnapshotFile, err)
	}

	var snapshotCM k8scorev1.ConfigMap

	if unmarshalErr := sigsyaml.Unmarshal(data, &snapshotCM); unmarshalErr != nil {
		return fmt.Errorf("failed parsing snapshot ConfigMap: %w", unmarshalErr)
	}

	// Group snapshot entries by node name.
	// Key format: <nodeName>__<fileName>; skip *__save-output entries.
	type nodeFile struct {
		nodeName string
		fileName string
		content  string
	}

	var nodeFiles []nodeFile

	for key, content := range snapshotCM.Data {
		parts := strings.SplitN(key, snapshotKeySeparator, 2)
		if len(parts) != 2 {
			u.logger.Warnf("unexpected snapshot key format %q, skipping", key)

			continue
		}

		nodeName, fileName := parts[0], parts[1]

		if fileName == "save-output" {
			continue
		}

		nodeFiles = append(nodeFiles, nodeFile{nodeName: nodeName, fileName: fileName, content: content})
	}

	// Write all extracted files.
	firstFilePerNode := map[string]string{} // nodeName → relative outPath of first written file

	for _, nf := range nodeFiles {
		outPath, writeErr := u.writeDeviceFile(nf.nodeName, nf.fileName, nf.content)
		if writeErr != nil {
			return writeErr
		}

		if _, seen := firstFilePerNode[nf.nodeName]; !seen {
			relPath, relErr := filepath.Rel(u.outputDirectory, outPath)
			if relErr != nil {
				relPath = outPath
			}

			firstFilePerNode[nf.nodeName] = relPath
		}
	}

	if topologyCR == nil {
		u.logger.Info(
			"no Topology CR available; device config files extracted but no containerlab YAML produced",
		)

		return nil
	}

	// Reconstruct containerlab YAML with updated startup-config paths.
	clabConfig, err := clabernetesutilcontainerlab.LoadContainerlabConfig(
		topologyCR.Spec.Definition.Containerlab,
	)
	if err != nil {
		return fmt.Errorf("failed parsing embedded containerlab definition: %w", err)
	}

	for nodeName, relPath := range firstFilePerNode {
		if node, ok := clabConfig.Topology.Nodes[nodeName]; ok {
			node.StartupConfig = relPath
		}
	}

	return u.writeClabYAML(clabConfig)
}

// writeDeviceFile writes content to <outputDirectory>/<nodeName>/<fileName> and returns the
// absolute path of the written file.
func (u *Unclabverter) writeDeviceFile(nodeName, fileName, content string) (string, error) {
	nodeDir := filepath.Join(u.outputDirectory, nodeName)

	if err := os.MkdirAll(nodeDir, clabernetesconstants.PermissionsEveryoneReadWriteOwnerExecute); err != nil {
		return "", fmt.Errorf("failed creating node directory %q: %w", nodeDir, err)
	}

	outPath := filepath.Join(nodeDir, fileName)

	u.logger.Debugf("writing device file: %s", outPath)

	if err := os.WriteFile(outPath, []byte(content), clabernetesconstants.PermissionsEveryoneRead); err != nil {
		return "", fmt.Errorf("failed writing file %q: %w", outPath, err)
	}

	return outPath, nil
}

// writeClabYAML marshals clabConfig and writes it to <outputDirectory>/<name>.clab.yaml.
// Uses gopkg.in/yaml.v3 directly so that yaml:"omitempty" struct tags are honoured, producing
// clean output that matches the containerlab YAML convention.
func (u *Unclabverter) writeClabYAML(clabConfig *clabernetesutilcontainerlab.Config) error {
	out, err := yaml.Marshal(clabConfig)
	if err != nil {
		return fmt.Errorf("failed marshaling containerlab config: %w", err)
	}

	outPath := filepath.Join(u.outputDirectory, clabConfig.Name+".clab.yaml")

	u.logger.Infof("writing containerlab topology: %s", outPath)

	if writeErr := os.WriteFile(
		outPath,
		out,
		clabernetesconstants.PermissionsEveryoneRead,
	); writeErr != nil {
		return fmt.Errorf("failed writing containerlab YAML: %w", writeErr)
	}

	return nil
}

