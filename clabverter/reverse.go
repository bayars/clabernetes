package clabverter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	clabernetesapis "github.com/srl-labs/clabernetes/apis"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	"gopkg.in/yaml.v3"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	sigsyaml "sigs.k8s.io/yaml"
)

const snapshotKeySeparator = "__"

// Unclabverter holds data and methods for the reverse conversion: from a clabverter output
// directory (or snapshot ConfigMap) back to a containerlab topology YAML and device config files
// organized as <NodeName>/<FileName>.
type Unclabverter struct {
	logger          claberneteslogging.Instance
	inputDirectory  string
	outputDirectory string
	// fromSnapshot is either a local file path to a snapshot ConfigMap YAML, or the name of a
	// Kubernetes ConfigMap to fetch from the cluster. An existing local file takes precedence.
	fromSnapshot string
	// namespace is the Kubernetes namespace used when fetching a snapshot by name from the cluster.
	// When empty the current kubeconfig context namespace is used.
	namespace string
}

// MustNewUnclabverter returns an instance of Unclabverter or panics.
func MustNewUnclabverter(
	inputDirectory,
	outputDirectory,
	fromSnapshot,
	namespace string,
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
		logger:          logger,
		inputDirectory:  inputDirectory,
		outputDirectory: outputDirectory,
		fromSnapshot:    fromSnapshot,
		namespace:       namespace,
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

	// Determine config source: snapshot (file or K8s) or output-directory ConfigMaps.
	if u.fromSnapshot != "" {
		snapshotCM, fetchedTopoCR, fetchErr := u.loadSnapshot()
		if fetchErr != nil {
			return fetchErr
		}

		// Prefer a Topology CR fetched from K8s over one found in the input directory.
		if fetchedTopoCR != nil {
			topologyCR = fetchedTopoCR
		}

		return u.unclabvertFromSnapshot(topologyCR, snapshotCM)
	}

	if topologyCR == nil {
		return fmt.Errorf(
			"no Topology CR found in input directory %q; "+
				"provide --input-directory with a clabverter output directory "+
				"or use --from-snapshot",
			u.inputDirectory,
		)
	}

	return u.unclabvertFromOutputDir(topologyCR, configMaps)
}

// loadSnapshot returns the snapshot ConfigMap and, when fetched from Kubernetes, the associated
// Topology CR (nil when reading from a local file). If fromSnapshot resolves to an existing local
// file it is read directly; otherwise the ConfigMap is fetched from the Kubernetes cluster and the
// Topology CR is fetched using the clabernetes/topologyOwner label on the ConfigMap.
func (u *Unclabverter) loadSnapshot() (*k8scorev1.ConfigMap, *StatuslessTopology, error) {
	if _, statErr := os.Stat(u.fromSnapshot); statErr == nil {
		u.logger.Debugf("loading snapshot from local file: %s", u.fromSnapshot)

		cm, err := u.loadSnapshotFromFile()

		return cm, nil, err
	}

	u.logger.Debugf(
		"snapshot %q is not a local file; fetching from Kubernetes cluster", u.fromSnapshot,
	)

	return u.fetchSnapshotFromKubernetes()
}

// loadSnapshotFromFile reads and parses a snapshot ConfigMap YAML from disk.
func (u *Unclabverter) loadSnapshotFromFile() (*k8scorev1.ConfigMap, error) {
	data, err := os.ReadFile(u.fromSnapshot) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed reading snapshot file %q: %w", u.fromSnapshot, err)
	}

	var cm k8scorev1.ConfigMap

	if err = sigsyaml.Unmarshal(data, &cm); err != nil {
		return nil, fmt.Errorf("failed parsing snapshot ConfigMap from %q: %w", u.fromSnapshot, err)
	}

	return &cm, nil //nolint:nilerr
}

// topologyGVR is the GroupVersionResource for the Topology CRD.
var topologyGVR = schema.GroupVersionResource{ //nolint:gochecknoglobals
	Group:    clabernetesapis.Group,
	Version:  "v1alpha1",
	Resource: "topologies",
}

// fetchSnapshotFromKubernetes fetches the snapshot ConfigMap by name from the Kubernetes cluster
// and also attempts to fetch the associated Topology CR using the clabernetes/topologyOwner label.
// The namespace is taken from the --namespace flag; if empty, the kubeconfig context namespace is
// used (falling back to "default").
func (u *Unclabverter) fetchSnapshotFromKubernetes() (*k8scorev1.ConfigMap, *StatuslessTopology, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{},
	)

	kubeConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed building kubeconfig for snapshot lookup: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating kubernetes client for snapshot lookup: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(kubeConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating dynamic kubernetes client: %w", err)
	}

	ns := u.namespace
	if ns == "" {
		ns, _, err = clientConfig.Namespace()
		if err != nil || ns == "" {
			ns = "default"
		}
	}

	u.logger.Infof("fetching snapshot ConfigMap %q from namespace %q", u.fromSnapshot, ns)

	cm, err := kubeClient.CoreV1().ConfigMaps(ns).Get(
		context.Background(),
		u.fromSnapshot,
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"failed fetching snapshot ConfigMap %q in namespace %q: %w",
			u.fromSnapshot, ns, err,
		)
	}

	// Attempt to fetch the Topology CR using the topologyOwner label on the snapshot ConfigMap.
	topologyName := cm.Labels[clabernetesconstants.LabelTopologyOwner]
	if topologyName == "" {
		u.logger.Info(
			"snapshot ConfigMap has no topologyOwner label; no containerlab YAML will be produced",
		)

		return cm, nil, nil
	}

	u.logger.Infof("fetching Topology CR %q from namespace %q", topologyName, ns)

	unstructuredTopo, err := dynamicClient.Resource(topologyGVR).Namespace(ns).Get(
		context.Background(),
		topologyName,
		metav1.GetOptions{},
	)
	if err != nil {
		u.logger.Warnf(
			"failed fetching Topology CR %q in namespace %q (skipping containerlab YAML): %s",
			topologyName, ns, err,
		)

		return cm, nil, nil
	}

	topoBytes, err := sigsyaml.Marshal(unstructuredTopo.Object)
	if err != nil {
		u.logger.Warnf("failed marshaling Topology CR (skipping containerlab YAML): %s", err)

		return cm, nil, nil
	}

	var topoCR StatuslessTopology

	if err = sigsyaml.Unmarshal(topoBytes, &topoCR); err != nil {
		u.logger.Warnf("failed parsing Topology CR (skipping containerlab YAML): %s", err)

		return cm, nil, nil
	}

	return cm, &topoCR, nil
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

// unclabvertFromSnapshot extracts the startup-config file for each node from a snapshot ConfigMap
// and optionally reconstructs the containerlab YAML if a Topology CR is available.
// Only the first non-save-output file per node is extracted (matching the forward direction's
// behaviour of using exactly one file per node as the startup config).
func (u *Unclabverter) unclabvertFromSnapshot(
	topologyCR *StatuslessTopology,
	snapshotCM *k8scorev1.ConfigMap,
) error {
	// Sort keys so that selection is deterministic across runs.
	sortedKeys := make([]string, 0, len(snapshotCM.Data))

	for key := range snapshotCM.Data {
		sortedKeys = append(sortedKeys, key)
	}

	sort.Strings(sortedKeys)

	// Extract the first non-save-output file per node.
	firstFilePerNode := map[string]string{} // nodeName → relative output path

	for _, key := range sortedKeys {
		parts := strings.SplitN(key, snapshotKeySeparator, 2)
		if len(parts) != 2 {
			u.logger.Warnf("unexpected snapshot key format %q, skipping", key)

			continue
		}

		nodeName, fileName := parts[0], parts[1]

		if fileName == "save-output" {
			continue
		}

		if _, seen := firstFilePerNode[nodeName]; seen {
			continue
		}

		outPath, writeErr := u.writeDeviceFile(nodeName, fileName, snapshotCM.Data[key])
		if writeErr != nil {
			return writeErr
		}

		relPath, relErr := filepath.Rel(u.outputDirectory, outPath)
		if relErr != nil {
			relPath = outPath
		}

		firstFilePerNode[nodeName] = relPath
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
