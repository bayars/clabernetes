package topology

import (
	"fmt"
	"maps"
	"reflect"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

// StartupConfigReconciler manages per-node seed ConfigMaps and startup-config PVCs.
// Every node that has a startup-config defined gets:
//   - a seed CM  (<topology>-<node>-startup-seed) holding the raw config content
//   - a PVC      (<topology>-<node>-startup-cfg)  as the permanent config store
//
// The launcher copies from the seed CM to the PVC on first start (if PVC is empty).
type StartupConfigReconciler struct {
	log                 claberneteslogging.Instance
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewStartupConfigReconciler returns a new StartupConfigReconciler.
func NewStartupConfigReconciler(
	log claberneteslogging.Instance,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *StartupConfigReconciler {
	return &StartupConfigReconciler{
		log:                 log,
		configManagerGetter: configManagerGetter,
	}
}

// nodesWithStartupConfig returns the subset of nodes (from clabernetesConfigs) that have a
// non-empty startup-config field.
func nodesWithStartupConfig(
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
) map[string]string {
	result := make(map[string]string)

	for nodeName, cfg := range clabernetesConfigs {
		content := cfg.Topology.GetNodeStartupConfig(nodeName)
		if content == "" {
			continue
		}

		result[nodeName] = content
	}

	return result
}

// ResolveSeedCMs returns an ObjectDiffer for seed ConfigMaps relative to which nodes currently
// have a startup config.
func (r *StartupConfigReconciler) ResolveSeedCMs(
	ownedCMs *k8scorev1.ConfigMapList,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
	owningTopology *clabernetesapisv1alpha1.Topology,
) (*clabernetesutil.ObjectDiffer[*k8scorev1.ConfigMap], error) {
	cms := &clabernetesutil.ObjectDiffer[*k8scorev1.ConfigMap]{
		Current: map[string]*k8scorev1.ConfigMap{},
	}

	suffix := clabernetesconstants.StartupConfigSeedCMSuffix

	for i := range ownedCMs.Items {
		labels := ownedCMs.Items[i].Labels
		if labels == nil {
			continue
		}

		nodeName, ok := labels[clabernetesconstants.LabelTopologyNode]
		if !ok || nodeName == "" {
			continue
		}

		// only manage CMs that belong to the startup-seed role
		if labels[clabernetesconstants.LabelTopologyRole] != suffix {
			continue
		}

		cms.Current[nodeName] = &ownedCMs.Items[i]
	}

	nodesWithCfg := nodesWithStartupConfig(clabernetesConfigs)
	allNodes := make([]string, 0, len(nodesWithCfg))

	for n := range nodesWithCfg {
		allNodes = append(allNodes, n)
	}

	cms.SetMissing(allNodes)
	cms.SetExtra(allNodes)

	return cms, nil
}

// ResolvePVCs returns an ObjectDiffer for startup-config PVCs relative to which nodes currently
// have a startup config.
func (r *StartupConfigReconciler) ResolvePVCs(
	ownedPVCs *k8scorev1.PersistentVolumeClaimList,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
	owningTopology *clabernetesapisv1alpha1.Topology,
) (*clabernetesutil.ObjectDiffer[*k8scorev1.PersistentVolumeClaim], error) {
	pvcs := &clabernetesutil.ObjectDiffer[*k8scorev1.PersistentVolumeClaim]{
		Current: map[string]*k8scorev1.PersistentVolumeClaim{},
	}

	suffix := clabernetesconstants.StartupConfigPVCSuffix

	for i := range ownedPVCs.Items {
		labels := ownedPVCs.Items[i].Labels
		if labels == nil {
			continue
		}

		nodeName, ok := labels[clabernetesconstants.LabelTopologyNode]
		if !ok || nodeName == "" {
			continue
		}

		if labels[clabernetesconstants.LabelTopologyRole] != suffix {
			continue
		}

		pvcs.Current[nodeName] = &ownedPVCs.Items[i]
	}

	nodesWithCfg := nodesWithStartupConfig(clabernetesConfigs)
	allNodes := make([]string, 0, len(nodesWithCfg))

	for n := range nodesWithCfg {
		allNodes = append(allNodes, n)
	}

	pvcs.SetMissing(allNodes)
	pvcs.SetExtra(allNodes)

	return pvcs, nil
}

// RenderSeedCM renders the seed ConfigMap for a node's startup config.
func (r *StartupConfigReconciler) RenderSeedCM(
	owningTopology *clabernetesapisv1alpha1.Topology,
	nodeName,
	startupConfigContent string,
) *k8scorev1.ConfigMap {
	owningTopologyName := owningTopology.GetName()

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	safeNodeName := clabernetesutilkubernetes.EnforceDNSLabelConvention(nodeName)

	name := clabernetesutilkubernetes.SafeConcatNameKubernetes(
		owningTopologyName,
		safeNodeName,
		clabernetesconstants.StartupConfigSeedCMSuffix,
	)

	labels := map[string]string{
		clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:          name,
		clabernetesconstants.LabelTopologyOwner: owningTopologyName,
		clabernetesconstants.LabelTopologyNode:  nodeName,
		clabernetesconstants.LabelTopologyRole:  clabernetesconstants.StartupConfigSeedCMSuffix,
		clabernetesconstants.LabelTopologyKind:  GetTopologyKind(owningTopology),
	}

	maps.Copy(labels, globalLabels)

	return &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   owningTopology.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
		Data: map[string]string{
			clabernetesconstants.StartupConfigFileName: startupConfigContent,
		},
	}
}

// ConformsSeedCM checks whether the existing seed CM matches the rendered one.
func (r *StartupConfigReconciler) ConformsSeedCM(
	existingCM,
	renderedCM *k8scorev1.ConfigMap,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	if !reflect.DeepEqual(existingCM.Data, renderedCM.Data) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingCM.ObjectMeta.Labels,
		renderedCM.ObjectMeta.Labels,
	) {
		return false
	}

	if len(existingCM.ObjectMeta.OwnerReferences) != 1 {
		return false
	}

	if existingCM.ObjectMeta.OwnerReferences[0].UID != expectedOwnerUID {
		return false
	}

	return true
}

// RenderStartupPVC renders the startup-config PVC for a node.
func (r *StartupConfigReconciler) RenderStartupPVC(
	owningTopology *clabernetesapisv1alpha1.Topology,
	nodeName string,
	existingPVC *k8scorev1.PersistentVolumeClaim,
) *k8scorev1.PersistentVolumeClaim {
	owningTopologyName := owningTopology.GetName()

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	safeNodeName := clabernetesutilkubernetes.EnforceDNSLabelConvention(nodeName)

	name := clabernetesutilkubernetes.SafeConcatNameKubernetes(
		owningTopologyName,
		safeNodeName,
		clabernetesconstants.StartupConfigPVCSuffix,
	)

	labels := map[string]string{
		clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:          name,
		clabernetesconstants.LabelTopologyOwner: owningTopologyName,
		clabernetesconstants.LabelTopologyNode:  nodeName,
		clabernetesconstants.LabelTopologyRole:  clabernetesconstants.StartupConfigPVCSuffix,
		clabernetesconstants.LabelTopologyKind:  GetTopologyKind(owningTopology),
	}

	maps.Copy(labels, globalLabels)

	// PVC size: topology → global config → compiled default
	pvcSizeStr := owningTopology.Spec.Deployment.StartupConfigPVCSize
	if pvcSizeStr == "" {
		pvcSizeStr = r.configManagerGetter().GetStartupConfigPVCSize()
	}

	if pvcSizeStr == "" {
		pvcSizeStr = clabernetesconstants.StartupConfigPVCDefaultSize
	}

	pvcSize, err := resource.ParseQuantity(pvcSizeStr)
	if err != nil {
		r.log.Warnf(
			"startup config PVC size %q failed parsing, using default %s",
			pvcSizeStr,
			clabernetesconstants.StartupConfigPVCDefaultSize,
		)

		pvcSize = resource.MustParse(clabernetesconstants.StartupConfigPVCDefaultSize)
	}

	// Storage class: topology → global config → cluster default (nil means use cluster default)
	storageClassName := owningTopology.Spec.Deployment.StartupConfigStorageClassName
	if storageClassName == "" {
		storageClassName = r.configManagerGetter().GetStartupConfigStorageClassName()
	}

	pvc := &k8scorev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   owningTopology.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: k8scorev1.PersistentVolumeClaimSpec{
			AccessModes: []k8scorev1.PersistentVolumeAccessMode{
				k8scorev1.ReadWriteOnce,
			},
			Resources: k8scorev1.VolumeResourceRequirements{
				Requests: k8scorev1.ResourceList{
					"storage": pvcSize,
				},
			},
			VolumeMode: clabernetesutil.ToPointer(k8scorev1.PersistentVolumeFilesystem),
		},
	}

	if storageClassName != "" {
		pvc.Spec.StorageClassName = &storageClassName
	}

	if existingPVC != nil {
		pvc.Spec.VolumeName = existingPVC.Spec.VolumeName
	}

	return pvc
}

// ConformsStartupPVC checks whether the existing startup-config PVC matches the rendered one.
func (r *StartupConfigReconciler) ConformsStartupPVC(
	existingPVC,
	renderedPVC *k8scorev1.PersistentVolumeClaim,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	existingSize := existingPVC.Spec.Resources.Requests.Storage().Value()
	renderedSize := renderedPVC.Spec.Resources.Requests.Storage().Value()

	if renderedSize != existingSize {
		if renderedSize > existingSize {
			return false
		}

		r.log.Warnf(
			"startup config PVC existing size %q is larger than desired %q; "+
				"PVC size can only be increased, ignoring shrink request",
			existingPVC.Spec.Resources.Requests.Storage().String(),
			renderedPVC.Spec.Resources.Requests.Storage().String(),
		)
	}

	// Storage class cannot be changed once a PVC is created; warn if it differs.
	renderedStorageClass := ""
	if renderedPVC.Spec.StorageClassName != nil {
		renderedStorageClass = *renderedPVC.Spec.StorageClassName
	}

	existingStorageClass := ""
	if existingPVC.Spec.StorageClassName != nil {
		existingStorageClass = *existingPVC.Spec.StorageClassName
	}

	if renderedStorageClass != "" && renderedStorageClass != existingStorageClass {
		r.log.Warnf(
			"startup config PVC storage class %q differs from existing %q; "+
				"storage class cannot be changed on an existing PVC — "+
				"delete the PVC to apply the new storage class",
			renderedStorageClass,
			existingStorageClass,
		)
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingPVC.ObjectMeta.Labels,
		renderedPVC.ObjectMeta.Labels,
	) {
		return false
	}

	if len(existingPVC.ObjectMeta.OwnerReferences) != 1 {
		return false
	}

	if existingPVC.ObjectMeta.OwnerReferences[0].UID != expectedOwnerUID {
		return false
	}

	return true
}

// StartupConfigPVCVolumeName returns the volume name used in the launcher pod for the startup-cfg
// PVC of a given node.
func StartupConfigPVCVolumeName(owningTopologyName, nodeName string) string {
	return fmt.Sprintf(
		"%s-%s-%s",
		clabernetesutilkubernetes.EnforceDNSLabelConvention(owningTopologyName),
		clabernetesutilkubernetes.EnforceDNSLabelConvention(nodeName),
		clabernetesconstants.StartupConfigPVCSuffix,
	)
}

// StartupConfigSeedCMVolumeName returns the volume name used in the launcher pod for the seed CM
// of a given node.
func StartupConfigSeedCMVolumeName(owningTopologyName, nodeName string) string {
	return fmt.Sprintf(
		"%s-%s-%s",
		clabernetesutilkubernetes.EnforceDNSLabelConvention(owningTopologyName),
		clabernetesutilkubernetes.EnforceDNSLabelConvention(nodeName),
		clabernetesconstants.StartupConfigSeedCMSuffix,
	)
}
