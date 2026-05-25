package constants

// Version of the clabernetes manager. Set with build flags, so leave at 0.0.0.
var Version = "0.0.0" //nolint: gochecknoglobals

const (
	// Clabernetes is the name... clabernetes.
	Clabernetes = "clabernetes"

	// Clabverter is a constant for the lovely name "clabverter".
	Clabverter = "clabverter"

	// True is a constant representing the string "true".
	True = "true"

	// False is a constant representing the string "false".
	False = "false"

	// Default is a constant for the string default -- often used for keys in clabernetes maps.
	Default = "default"

	// AppNameDefault is the default name for the "app" (the helm value appName) -- "clabernetes".
	AppNameDefault = "clabernetes"

	// VXLANServicePort is the port number for vxlan that we use in the kubernetes service.
	VXLANServicePort = 14789

	// MgmtBridgeVNID is the VXLAN Network Identifier used for the management bridge segment that
	// connects all launcher pods' containerlab management bridges into a single L2 domain. This
	// value sits just above the point-to-point tunnel VNID range (1–15,999,999).
	MgmtBridgeVNID = 16_000_000

	// SlurpeethServicePort is the port number for slurpeeth that we use in the kubernetes service.
	SlurpeethServicePort = 4799

	// TCP is... TCP.
	TCP = "TCP"

	// UDP is... UDP.
	UDP = "UDP"

	// FileModeRead is "read". Used for configmap mount permissions in the
	// TopologySpec/FilesFromConfigMap.
	FileModeRead = "read"

	// FileModeExecute is "execute". Used for configmap mount permissions in the
	// TopologySpec/FilesFromConfigMap.
	FileModeExecute = "execute"

	// HostKeyword is the containerlab reserved keyword to define host links endpoints.
	HostKeyword = "host"
)
