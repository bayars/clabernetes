package snapshot

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
	"gopkg.in/yaml.v3"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Reconcile handles reconciliation for the Snapshot controller.
func (c *Controller) Reconcile(
	ctx context.Context,
	req ctrlruntime.Request,
) (ctrlruntime.Result, error) {
	c.BaseController.LogReconcileStart(req)

	snapshot := &clabernetesapisv1alpha1.Snapshot{}

	err := c.BaseController.Client.Get(ctx, req.NamespacedName, snapshot)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			c.BaseController.LogReconcileCompleteObjectNotExist(req)

			return ctrlruntime.Result{}, nil
		}

		c.BaseController.LogReconcileFailedGettingObject(req, err)

		return ctrlruntime.Result{}, err
	}

	if snapshot.DeletionTimestamp != nil {
		return ctrlruntime.Result{}, nil
	}

	// Skip already-terminal snapshots.
	if snapshot.Status.Phase == clabernetesapisv1alpha1.SnapshotPhaseCompleted ||
		snapshot.Status.Phase == clabernetesapisv1alpha1.SnapshotPhasePartiallySuccessful ||
		snapshot.Status.Phase == clabernetesapisv1alpha1.SnapshotPhaseFailed {
		c.BaseController.LogReconcileCompleteSuccess(req)

		return ctrlruntime.Result{}, nil
	}

	result, err := c.reconcileSnapshot(ctx, snapshot)
	if err != nil {
		return result, err
	}

	c.BaseController.LogReconcileCompleteSuccess(req)

	return result, nil
}

func (c *Controller) reconcileSnapshot(
	ctx context.Context,
	snapshot *clabernetesapisv1alpha1.Snapshot,
) (ctrlruntime.Result, error) {
	err := c.setSnapshotRunning(ctx, snapshot)
	if err != nil {
		return ctrlruntime.Result{}, err
	}

	topology, topologyNamespace, err := c.fetchReferencedTopology(ctx, snapshot)
	if err != nil {
		return c.failSnapshot(ctx, snapshot, err.Error())
	}

	nodeNames := make([]string, 0, len(topology.Status.NodeReadiness))
	for nodeName := range topology.Status.NodeReadiness {
		nodeNames = append(nodeNames, nodeName)
	}

	if len(nodeNames) == 0 {
		return c.failSnapshot(ctx, snapshot, "topology has no nodes in NodeReadiness status")
	}

	clabConfig := c.parseContainerlabTopology(topology)
	nodeNames = c.filterSnapshotableNodes(nodeNames, snapshot.Spec.TopologyRef, clabConfig)

	if len(nodeNames) == 0 {
		return c.failSnapshot(ctx, snapshot, "topology has no snapshotable nodes (all nodes are unsupported kinds)")
	}

	configMapData, nodeConfigs, failedNodes := c.collectNodeSnapshots(
		ctx, snapshot, topologyNamespace, nodeNames,
	)

	return c.finalizeSnapshot(ctx, snapshot, topology, configMapData, nodeConfigs, failedNodes)
}

func (c *Controller) setSnapshotRunning(
	ctx context.Context,
	snapshot *clabernetesapisv1alpha1.Snapshot,
) error {
	snapshot.Status.Phase = clabernetesapisv1alpha1.SnapshotPhaseRunning

	err := c.BaseController.Client.Status().Update(ctx, snapshot)
	if err != nil {
		c.BaseController.Log.Warnf(
			"failed updating snapshot '%s/%s' status to Running, error: %s",
			snapshot.Namespace,
			snapshot.Name,
			err,
		)
	}

	return err
}

func (c *Controller) fetchReferencedTopology(
	ctx context.Context,
	snapshot *clabernetesapisv1alpha1.Snapshot,
) (*clabernetesapisv1alpha1.Topology, string, error) {
	topologyNamespace := snapshot.Spec.TopologyNamespace
	if topologyNamespace == "" {
		topologyNamespace = snapshot.Namespace
	}

	topology := &clabernetesapisv1alpha1.Topology{}

	err := c.BaseController.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: topologyNamespace,
			Name:      snapshot.Spec.TopologyRef,
		},
		topology,
	)
	if err != nil {
		return nil, "", fmt.Errorf(
			"failed fetching topology '%s/%s': %w",
			topologyNamespace,
			snapshot.Spec.TopologyRef,
			err,
		)
	}

	return topology, topologyNamespace, nil
}

func (c *Controller) collectNodeSnapshots(
	ctx context.Context,
	snapshot *clabernetesapisv1alpha1.Snapshot,
	topologyNamespace string,
	nodeNames []string,
) (configMapData map[string]string, nodeConfigs map[string][]string, failedNodes map[string]string) {
	configMapData = make(map[string]string)
	nodeConfigs = make(map[string][]string)
	failedNodes = make(map[string]string)

	for _, nodeName := range nodeNames {
		c.BaseController.Log.Infof(
			"saving node %q in topology %q",
			nodeName,
			snapshot.Spec.TopologyRef,
		)

		targetPod := c.findRunningPod(ctx, topologyNamespace, snapshot.Spec.TopologyRef, nodeName)
		if targetPod == nil {
			failedNodes[nodeName] = "no running pod found"

			continue
		}

		nodeFileKeys, nodeErr := c.saveAndCollectNodeFiles(
			ctx, topologyNamespace, targetPod.Name, nodeName,
			clabernetesutilkubernetes.EnforceDNSLabelConvention(nodeName), snapshot.Name, configMapData,
		)
		if nodeErr != nil {
			failedNodes[nodeName] = nodeErr.Error()
		}

		if len(nodeFileKeys) > 0 {
			nodeConfigs[nodeName] = nodeFileKeys
		}
	}

	return configMapData, nodeConfigs, failedNodes
}

func (c *Controller) findRunningPod(
	ctx context.Context,
	namespace, topologyRef, nodeName string,
) *k8scorev1.Pod {
	podList := &k8scorev1.PodList{}

	err := c.BaseController.Client.List(
		ctx,
		podList,
		ctrlruntimeclient.InNamespace(namespace),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelTopologyOwner: topologyRef,
			clabernetesconstants.LabelTopologyNode:  nodeName,
		},
	)
	if err != nil {
		c.BaseController.Log.Warnf(
			"failed listing pods for node %q: %s, skipping",
			nodeName,
			err,
		)

		return nil
	}

	if len(podList.Items) == 0 {
		c.BaseController.Log.Warnf("no pods found for node %q, skipping", nodeName)

		return nil
	}

	for idx := range podList.Items {
		if podList.Items[idx].Status.Phase == k8scorev1.PodRunning {
			return &podList.Items[idx]
		}
	}

	c.BaseController.Log.Warnf("no running pod found for node %q, skipping", nodeName)

	return nil
}

func (c *Controller) saveAndCollectNodeFiles(
	ctx context.Context,
	namespace, podName, nodeName, containerName, snapshotName string,
	configMapData map[string]string,
) ([]string, error) {
	saveOutput, saveErr := c.execInPod(
		ctx,
		namespace,
		podName,
		containerName,
		[]string{
			"sh",
			"-c",
			"cd /clabernetes && containerlab save -t topo.clab.yaml 2>&1",
		},
	)
	if saveErr != nil {
		c.BaseController.Log.Warnf("containerlab save failed for node %q: %s", nodeName, saveErr)
	}

	// Always store save output in the CM — it is small diagnostic text, never config content.
	configMapData[fmt.Sprintf("%s__save-output", nodeName)] = saveOutput

	// Use || true so a missing directory (node kind doesn't support save) never exits non-zero.
	savedFilesOutput, listErr := c.execInPod(
		ctx,
		namespace,
		podName,
		containerName,
		[]string{
			"sh",
			"-c",
			fmt.Sprintf(
				"dir=/clabernetes/clab-clabernetes-%s/%s/; [ -d \"$dir\" ] && find \"$dir\" -type f 2>/dev/null || true",
				nodeName,
				nodeName,
			),
		},
	)
	if listErr != nil {
		c.BaseController.Log.Warnf(
			"failed listing saved files for node %q: %s",
			nodeName,
			listErr,
		)

		if saveErr != nil {
			return nil, fmt.Errorf("containerlab save: %w; find config files: %w", saveErr, listErr)
		}

		return nil, listErr
	}

	savedFiles := strings.Split(strings.TrimSpace(savedFilesOutput), "\n")

	nodeFileKeys := make([]string, 0, len(savedFiles))

	for _, filePath := range savedFiles {
		filePath = strings.TrimSpace(filePath)
		if filePath == "" {
			continue
		}

		fileName := filePath[strings.LastIndex(filePath, "/")+1:]

		// Archive the file into the startup-cfg PVC instead of inlining it in the ConfigMap.
		// The CM only records the archive path so operators know where to find the snapshot.
		archivePath, archiveErr := c.archiveFileInPVC(
			ctx, namespace, podName, containerName, nodeName, snapshotName, filePath, fileName,
		)
		if archiveErr != nil {
			c.BaseController.Log.Warnf(
				"failed archiving file %q for node %q into PVC: %s — falling back to ConfigMap",
				filePath,
				nodeName,
				archiveErr,
			)

			// Fallback: read content and store in CM so the snapshot is not empty.
			fileContent, readErr := c.execInPod(
				ctx, namespace, podName, containerName, []string{"cat", filePath},
			)
			if readErr != nil {
				c.BaseController.Log.Warnf(
					"fallback CM read of %q for node %q also failed: %s",
					filePath,
					nodeName,
					readErr,
				)

				continue
			}

			key := fmt.Sprintf("%s__%s", nodeName, fileName)
			configMapData[key] = fileContent
			nodeFileKeys = append(nodeFileKeys, key)

			continue
		}

		// Store only the PVC archive path reference — no config content in the CM.
		key := fmt.Sprintf("%s__%s__pvc-path", nodeName, fileName)
		configMapData[key] = archivePath
		nodeFileKeys = append(nodeFileKeys, key)
	}

	// If save failed and no config files were found, treat as a warning rather than an error —
	// some node kinds (e.g. linux) do not support "containerlab save" and produce no files.
	if len(nodeFileKeys) == 0 {
		if saveErr != nil {
			c.BaseController.Log.Warnf(
				"containerlab save produced no files for node %q (node kind may not support save): %s",
				nodeName,
				saveErr,
			)
		}

		return nodeFileKeys, nil
	}

	return nodeFileKeys, nil
}

// archiveFileInPVC copies a saved config file into two locations inside the startup-cfg PVC:
//  1. The live path (/clabernetes/startup-cfg/startup-config) so the next launcher restart
//     picks up the latest running config.
//  2. An archive path (/clabernetes/startup-cfg/archive/<snapshotName>/<fileName>) for
//     point-in-time history without consuming ConfigMap space.
//
// Returns the archive path on success. Best-effort — caller falls back to CM on error.
func (c *Controller) archiveFileInPVC(
	ctx context.Context,
	namespace, podName, containerName, nodeName, snapshotName, srcPath, fileName string,
) (string, error) {
	archiveDir := fmt.Sprintf(
		"%s/archive/%s",
		clabernetesconstants.StartupConfigPVCMountPath,
		snapshotName,
	)

	archivePath := fmt.Sprintf("%s/%s", archiveDir, fileName)

	liveConfigPath := fmt.Sprintf(
		"%s/%s",
		clabernetesconstants.StartupConfigPVCMountPath,
		clabernetesconstants.StartupConfigFileName,
	)

	// Only proceed if the PVC mount exists.
	archiveCmd := fmt.Sprintf(
		"if [ -d '%s' ]; then "+
			"mkdir -p '%s' && "+
			"cp '%s' '%s' && "+
			"cp '%s' '%s' && "+
			"echo '%s'; fi",
		clabernetesconstants.StartupConfigPVCMountPath,
		archiveDir,
		srcPath, archivePath,
		srcPath, liveConfigPath,
		archivePath,
	)

	out, err := c.execInPod(
		ctx,
		namespace,
		podName,
		containerName,
		[]string{"sh", "-c", archiveCmd},
	)
	if err != nil {
		return "", err
	}

	result := strings.TrimSpace(out)
	if result == "" {
		// PVC not mounted for this node — no startup-config PVC, skip silently.
		return "", fmt.Errorf("startup-cfg PVC not mounted for node %q", nodeName)
	}

	c.BaseController.Log.Infof(
		"snapshot %q: archived config for node %q → live: %q, archive: %q",
		snapshotName,
		nodeName,
		liveConfigPath,
		archivePath,
	)

	return result, nil
}

func (c *Controller) finalizeSnapshot(
	ctx context.Context,
	snapshot *clabernetesapisv1alpha1.Snapshot,
	topology *clabernetesapisv1alpha1.Topology,
	configMapData map[string]string,
	nodeConfigs map[string][]string,
	failedNodes map[string]string,
) (ctrlruntime.Result, error) {
	timestamp := time.Now().UTC().Format(time.RFC3339)

	configMap := &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshot.Name,
			Namespace: snapshot.Namespace,
			Labels: map[string]string{
				clabernetesconstants.LabelTopologyOwner: snapshot.Spec.TopologyRef,
			},
			Annotations: map[string]string{
				clabernetesconstants.AnnotationSnapshotTimestamp: timestamp,
			},
		},
		Data: configMapData,
	}

	err := ctrlruntimeutil.SetOwnerReference(snapshot, configMap, c.BaseController.Client.Scheme())
	if err != nil {
		return c.failSnapshot(
			ctx,
			snapshot,
			fmt.Sprintf("failed setting owner reference on ConfigMap: %s", err),
		)
	}

	err = c.BaseController.Client.Create(ctx, configMap)
	if err != nil {
		if !apimachineryerrors.IsAlreadyExists(err) {
			return c.failSnapshot(
				ctx,
				snapshot,
				fmt.Sprintf("failed creating ConfigMap %q: %s", snapshot.Name, err),
			)
		}

		// ConfigMap already exists — update its data so we don't lose collected configs.
		patchBytes := buildConfigMapDataPatch(configMapData)

		patchErr := c.BaseController.Client.Patch(
			ctx,
			configMap,
			ctrlruntimeclient.RawPatch(apimachinerytypes.MergePatchType, patchBytes),
		)
		if patchErr != nil {
			c.BaseController.Log.Warnf(
				"failed patching existing ConfigMap %q: %s",
				snapshot.Name,
				patchErr,
			)
		}
	}

	// Determine phase based on per-node outcomes.
	var phase string

	switch {
	case len(failedNodes) == 0:
		phase = clabernetesapisv1alpha1.SnapshotPhaseCompleted
	case len(nodeConfigs) > 0:
		phase = clabernetesapisv1alpha1.SnapshotPhasePartiallySuccessful
	default:
		phase = clabernetesapisv1alpha1.SnapshotPhaseFailed
	}

	snapshot.Status.Phase = phase
	snapshot.Status.ConfigMapRef = snapshot.Name
	snapshot.Status.Timestamp = timestamp
	snapshot.Status.NodeConfigs = nodeConfigs

	if len(failedNodes) > 0 {
		snapshot.Status.FailedNodes = failedNodes
		snapshot.Status.Message = buildFailedNodesMessage(failedNodes, len(nodeConfigs))
	}

	err = c.BaseController.Client.Status().Update(ctx, snapshot)
	if err != nil {
		c.BaseController.Log.Warnf(
			"failed updating snapshot '%s/%s' status to %s, error: %s",
			snapshot.Namespace,
			snapshot.Name,
			phase,
			err,
		)

		return ctrlruntime.Result{}, err
	}

	c.patchTopologySnapshotAnnotations(ctx, topology, snapshot.Name, timestamp)

	return ctrlruntime.Result{}, nil
}

// buildFailedNodesMessage returns a human-readable summary of node failures.
func buildFailedNodesMessage(failedNodes map[string]string, successCount int) string {
	parts := make([]string, 0, len(failedNodes))
	for node, reason := range failedNodes {
		parts = append(parts, fmt.Sprintf("%s (%s)", node, reason))
	}

	return fmt.Sprintf(
		"%d/%d nodes failed: %s",
		len(failedNodes),
		len(failedNodes)+successCount,
		strings.Join(parts, ", "),
	)
}

// buildConfigMapDataPatch constructs a merge-patch payload that sets the data field.
func buildConfigMapDataPatch(data map[string]string) []byte {
	if len(data) == 0 {
		return []byte(`{"data":{}}`)
	}

	var sb strings.Builder

	sb.WriteString(`{"data":{`)

	first := true

	for k, v := range data {
		if !first {
			sb.WriteByte(',')
		}

		sb.WriteString(fmt.Sprintf("%q:%q", k, v))

		first = false
	}

	sb.WriteString(`}}`)

	return []byte(sb.String())
}

func (c *Controller) patchTopologySnapshotAnnotations(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	snapshotName, timestamp string,
) {
	patchBytes := fmt.Appendf(
		nil,
		`{"metadata":{"annotations":{%q:%q,%q:%q}}}`,
		clabernetesconstants.AnnotationSnapshotTimestamp,
		timestamp,
		clabernetesconstants.AnnotationSnapshotLatest,
		snapshotName,
	)

	err := c.BaseController.Client.Patch(
		ctx,
		topology,
		ctrlruntimeclient.RawPatch(apimachinerytypes.MergePatchType, patchBytes),
	)
	if err != nil {
		c.BaseController.Log.Warnf(
			"failed patching topology annotations with snapshot info: %s",
			err,
		)
	}
}

// failSnapshot sets the Snapshot status to Failed with the given message and returns.
func (c *Controller) failSnapshot(
	ctx context.Context,
	snapshot *clabernetesapisv1alpha1.Snapshot,
	message string,
) (ctrlruntime.Result, error) {
	c.BaseController.Log.Warnf(
		"snapshot '%s/%s' failed: %s", snapshot.Namespace, snapshot.Name, message,
	)

	snapshot.Status.Phase = clabernetesapisv1alpha1.SnapshotPhaseFailed
	snapshot.Status.Message = message

	err := c.BaseController.Client.Status().Update(ctx, snapshot)
	if err != nil {
		c.BaseController.Log.Warnf(
			"failed updating snapshot '%s/%s' status to Failed, error: %s",
			snapshot.Namespace,
			snapshot.Name,
			err,
		)
	}

	return ctrlruntime.Result{}, nil
}

// parseContainerlabTopology parses the raw containerlab topology YAML from the Topology spec.
// Returns nil if the topology is not a containerlab type (e.g. KNE) or parsing fails.
func (c *Controller) parseContainerlabTopology(
	topology *clabernetesapisv1alpha1.Topology,
) *clabernetesutilcontainerlab.Config {
	raw := topology.Spec.Definition.Containerlab
	if raw == "" {
		return nil
	}

	clabConfig := &clabernetesutilcontainerlab.Config{}

	if err := yaml.Unmarshal([]byte(raw), clabConfig); err != nil {
		c.BaseController.Log.Warnf("failed parsing containerlab topology for kind filtering: %s", err)

		return nil
	}

	if clabConfig.Topology == nil {
		return clabConfig
	}

	if clabConfig.Topology.Defaults == nil {
		clabConfig.Topology.Defaults = &clabernetesutilcontainerlab.NodeDefinition{}
	}

	return clabConfig
}

// filterSnapshotableNodes removes nodes whose containerlab kind does not support
// `containerlab save` (e.g. "linux"). If clabConfig is nil the list is returned unchanged.
func (c *Controller) filterSnapshotableNodes(
	nodeNames []string,
	topologyRef string,
	clabConfig *clabernetesutilcontainerlab.Config,
) []string {
	if clabConfig == nil || clabConfig.Topology == nil {
		return nodeNames
	}

	filtered := make([]string, 0, len(nodeNames))

	for _, name := range nodeNames {
		kind, _ := clabConfig.Topology.GetNodeKindType(name)
		if kind == "linux" {
			c.BaseController.Log.Debugf(
				"skipping node %q in topology %q: kind %q does not support containerlab save",
				name,
				topologyRef,
				kind,
			)

			continue
		}

		filtered = append(filtered, name)
	}

	return filtered
}

// execInPod executes a command in the specified container of a pod and returns stdout output.
func (c *Controller) execInPod(
	ctx context.Context,
	namespace,
	podName,
	containerName string,
	command []string,
) (string, error) {
	req := c.KubeClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec")

	req.VersionedParams(
		&k8scorev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		},
		k8sscheme.ParameterCodec,
	)

	exec, err := remotecommand.NewSPDYExecutor(c.BaseController.Config, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("failed creating SPDY executor: %w", err)
	}

	var stdout, stderr bytes.Buffer

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return stdout.String(), fmt.Errorf(
			"exec command failed: %w (stderr: %s)",
			err,
			stderr.String(),
		)
	}

	return stdout.String(), nil
}
