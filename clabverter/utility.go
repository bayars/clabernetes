package clabverter

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	sigsyaml "sigs.k8s.io/yaml"
)

// UtilityManifest is the top-level structure of the utility manifest YAML file.
type UtilityManifest struct {
	UtilityNodes map[string]UtilityNodeDefinition `yaml:"utilityNodes"`
}

// UtilityNodeDefinition describes a single utility node to inject into the topology.
type UtilityNodeDefinition struct {
	Image  string                   `yaml:"image"`
	Expose *UtilityNodeExposeConfig `yaml:"expose,omitempty"`
}

// UtilityNodeExposeConfig holds the per-node expose service override configuration.
type UtilityNodeExposeConfig struct {
	// Type overrides the expose service type. Valid values: None, ClusterIP, LoadBalancer.
	Type string `yaml:"type,omitempty"`
	// Annotations are merged into the expose service metadata.annotations.
	Annotations map[string]string `yaml:"annotations,omitempty"`
	// Ports lists the ports to expose. When set, auto-discovered ports are suppressed.
	Ports []UtilityPort `yaml:"ports,omitempty"`
	// DisableAutoExpose suppresses automatic port discovery for this node.
	DisableAutoExpose bool `yaml:"disableAutoExpose,omitempty"`
}

// UtilityPort is a port/protocol pair to expose on the utility node's service.
type UtilityPort struct {
	Port     int    `yaml:"port"`
	Protocol string `yaml:"protocol"` // TCP or UDP
}

func (c *Clabverter) loadUtilityManifest() error {
	if c.utilityManifestFile == "" {
		return nil
	}

	absPath, err := resolveFilePath(c.utilityManifestFile)
	if err != nil {
		return fmt.Errorf("resolving utility manifest path: %w", err)
	}

	c.utilityManifestFilePath = absPath

	raw, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("reading utility manifest %q: %w", absPath, err)
	}

	var manifest UtilityManifest

	if err = sigsyaml.UnmarshalStrict(raw, &manifest); err != nil {
		return fmt.Errorf("parsing utility manifest %q: %w", absPath, err)
	}

	c.utilityManifest = &manifest

	return nil
}

func (c *Clabverter) injectUtilityNodes() error {
	if c.utilityManifest == nil || len(c.utilityManifest.UtilityNodes) == 0 {
		return nil
	}

	if c.clabConfig.Topology == nil {
		c.clabConfig.Topology = &clabernetesutilcontainerlab.Topology{}
	}

	if c.clabConfig.Topology.Nodes == nil {
		c.clabConfig.Topology.Nodes = make(map[string]*clabernetesutilcontainerlab.NodeDefinition)
	}

	for name, def := range c.utilityManifest.UtilityNodes {
		ip, err := c.allocateNextIP()
		if err != nil {
			return fmt.Errorf("allocating mgmt-ipv4 for utility node %q: %w", name, err)
		}

		node := &clabernetesutilcontainerlab.NodeDefinition{
			Kind:     "linux",
			Image:    def.Image,
			MgmtIPv4: ip,
			Labels:   buildExposeLabels(name, def.Expose),
		}

		c.clabConfig.Topology.Nodes[name] = node

		c.logger.Debugf("injected utility node %q with mgmt-ipv4 %s", name, ip)
	}

	return nil
}

// allocateNextIP finds the next unused host address in the topology's management subnet.
func (c *Clabverter) allocateNextIP() (string, error) {
	subnet := "172.20.20.0/24"

	if c.clabConfig.Mgmt != nil && c.clabConfig.Mgmt.IPv4Subnet != "" {
		subnet = c.clabConfig.Mgmt.IPv4Subnet
	}

	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("parsing mgmt subnet %q: %w", subnet, err)
	}

	used := make(map[string]struct{})

	for _, node := range c.clabConfig.Topology.Nodes {
		if node.MgmtIPv4 != "" {
			used[node.MgmtIPv4] = struct{}{}
		}
	}

	// iterate host addresses starting from .2 (.1 is typically the gateway)
	candidate := cloneIP(ipNet.IP)
	candidate = incrementIP(candidate) // move to .1
	candidate = incrementIP(candidate) // move to .2

	for ipNet.Contains(candidate) {
		addr := candidate.String()

		if _, taken := used[addr]; !taken {
			used[addr] = struct{}{} // reserve it for subsequent calls
			return addr, nil
		}

		candidate = incrementIP(candidate)
	}

	return "", fmt.Errorf("no free addresses in management subnet %s", subnet)
}

func cloneIP(ip net.IP) net.IP {
	clone := make(net.IP, len(ip))
	copy(clone, ip)

	return clone
}

func incrementIP(ip net.IP) net.IP {
	result := cloneIP(ip)

	for i := len(result) - 1; i >= 0; i-- {
		result[i]++
		if result[i] != 0 {
			break
		}
	}

	return result
}

func buildExposeLabels(nodeName string, expose *UtilityNodeExposeConfig) map[string]string {
	labels := map[string]string{
		clabernetesconstants.LabelUtilityNode: nodeName,
	}

	if expose == nil {
		return labels
	}

	if expose.Type != "" {
		labels[clabernetesconstants.LabelUtilityExposeType] = expose.Type
	}

	if expose.DisableAutoExpose {
		labels[clabernetesconstants.LabelUtilityDisableAutoExpose] = clabernetesconstants.True
	}

	if len(expose.Ports) > 0 {
		portStrs := make([]string, 0, len(expose.Ports))
		for _, p := range expose.Ports {
			proto := p.Protocol
			if proto == "" {
				proto = clabernetesconstants.TCP
			}

			portStrs = append(portStrs, fmt.Sprintf("%d/%s", p.Port, proto))
		}

		labels[clabernetesconstants.LabelUtilityExposePorts] = strings.Join(portStrs, ",")
	}

	if len(expose.Annotations) > 0 {
		annotJSON, err := json.Marshal(expose.Annotations)
		if err == nil {
			labels[clabernetesconstants.LabelUtilityExposeAnnotations] = string(annotJSON)
		}
	}

	return labels
}

func resolveFilePath(path string) (string, error) {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}

		path = home + path[1:]
	}

	return path, nil
}
