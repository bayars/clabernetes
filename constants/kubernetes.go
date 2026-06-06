package constants

const (
	// KubernetesConfigMap is a const to use for "configmap".
	KubernetesConfigMap = "configmap"

	// KubernetesService is a const to use for "service".
	KubernetesService = "service"

	// KubernetesPVC is a const to use for "persistentvolumeclaim".
	KubernetesPVC = "persistentvolumeclaim"

	// KubernetesDeployment is a const to use for "deployment".
	KubernetesDeployment = "deployment"
)

const (
	// KubernetesDefaultInClusterDNSSuffix is the default in cluster dns suffix (duh).
	KubernetesDefaultInClusterDNSSuffix = "svc.cluster.local"
)

const (
	// KubernetesImagePullIfNotPresent holds the constant for "IfNotPresent" image pull policy.
	KubernetesImagePullIfNotPresent = "IfNotPresent"
)

const (
	// KubernetesCRIUnknown is a const for when we dont know what the CRI type is in a cluster.
	KubernetesCRIUnknown = "unknown"
	// KubernetesCRIContainerd is a const for the "containerd" type of CRI in a cluster.
	KubernetesCRIContainerd = "containerd"
	// KubernetesCRICrio is a const for the "cri-o" type of CRI in a cluster.
	KubernetesCRICrio = "crio"
)

const (
	// KubernetesCRISockContainerdPath is the path where the containerd sock lives.
	KubernetesCRISockContainerdPath = "/run/containerd"
	// KubernetesCRISockContainerd is the containerd sock filename.
	KubernetesCRISockContainerd = "containerd.sock"
)

const (
	// LauncherCRISockPath is the path where, if configured, the CRI sock is mounted in launcher
	// pods.
	LauncherCRISockPath = "/clabernetes/.node"
)

const (
	// MaxConfigMapDataBytes is a conservative byte limit for a single ConfigMap key's value.
	// Kubernetes/etcd hard-limits ConfigMaps to ~1MB total; we leave a buffer for metadata.
	MaxConfigMapDataBytes = 900_000

	// StartupConfigSeedCMSuffix is the suffix appended to topology+node names for the per-node
	// seed ConfigMap that holds the raw startup-config content.
	StartupConfigSeedCMSuffix = "startup-seed"

	// StartupConfigPVCSuffix is the suffix appended to topology+node names for the per-node PVC
	// that permanently stores the startup-config for the launcher.
	StartupConfigPVCSuffix = "startup-cfg"

	// NodePersistencePVCRole is the LabelTopologyRole value stamped on regular (node-persistence)
	// PVCs so that the PVC reconciler can filter them out from startup-config PVCs, which share
	// the same LabelTopologyOwner and LabelTopologyNode labels.
	NodePersistencePVCRole = "node"

	// StartupConfigPVCDefaultSize is the default PVC size for per-node startup config PVCs.
	StartupConfigPVCDefaultSize = "50Mi"

	// StartupConfigSeedMountPath is where the seed ConfigMap is mounted inside the launcher pod.
	StartupConfigSeedMountPath = "/clabernetes/startup-seed"

	// StartupConfigPVCMountPath is where the startup-config PVC is mounted inside the launcher pod.
	StartupConfigPVCMountPath = "/clabernetes/startup-cfg"

	// StartupConfigFileName is the key/filename used for startup config content.
	StartupConfigFileName = "startup-config"

	// NodePersistencePVCRole is the LabelTopologyRole value stamped on regular (node-persistence)
	// PVCs so that the PVC reconciler can filter them out from startup-config PVCs, which share
	// the same LabelTopologyOwner and LabelTopologyNode labels.
	NodePersistencePVCRole = "node"
)
