//  Copyright Istio Authors
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package kube

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mitchellh/go-homedir"

	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/scopes"
)

const (
	defaultKubeConfig = "~/.kube/config"
)

var (
	// The KUBECONFIG value from the environment.
	kubeConfigsFromEnv = getKubeConfigsFromEnvironmentOrDefault()
	// Settings we will collect from the command-line.
	settingsFromCommandLine = &Settings{
		KubeConfig:            kubeConfigsFromEnv,
		LoadBalancerSupported: true,
	}
	// hold kubeconfigs from command line to split later
	kubeConfigs string
	// hold controlPlaneTopology from command line to parse later
	controlPlaneTopology string
	// hold networkTopology from command line to parse later
	networkTopology string
	// hold configTopology from command line to parse later
	configTopology string
)

// NewSettingsFromCommandLine returns Settings obtained from command-line flags.
// flag.Parse must be called before calling this function.
func NewSettingsFromCommandLine() (*Settings, error) {
	if !flag.Parsed() {
		panic("flag.Parse must be called before this function")
	}

	s := settingsFromCommandLine.clone()

	var err error
	s.KubeConfig, err = parseKubeConfigs(kubeConfigs, ",")
	if err != nil {
		return nil, fmt.Errorf("kubeconfig: %v", err)
	}
	if len(s.KubeConfig) == 0 {
		s.KubeConfig = kubeConfigsFromEnv
	}

	s.ControlPlaneTopology, err = newControlPlaneTopology(s.KubeConfig)
	if err != nil {
		return nil, err
	}

	s.networkTopology, err = parseNetworkTopology(s.KubeConfig)
	if err != nil {
		return nil, err
	}

	s.ConfigTopology, err = newConfigTopology(s.KubeConfig, s.ControlPlaneTopology)
	if err != nil {
		return nil, err
	}

	return s, nil
}

func getKubeConfigsFromEnvironmentOrDefault() []string {
	// Normalize KUBECONFIG so that it is separated by the OS path list separator.
	// The framework currently supports comma as a separator, but that violates the
	// KUBECONFIG spec.
	value := env.KUBECONFIG.Value()
	if strings.Contains(value, ",") {
		updatedValue := strings.ReplaceAll(value, ",", string(filepath.ListSeparator))
		_ = os.Setenv(env.KUBECONFIG.Name(), updatedValue)
		scopes.Framework.Warnf("KUBECONFIG contains commas: %s.\nReplacing with %s: %s", value,
			filepath.ListSeparator, updatedValue)
		value = updatedValue
	}
	scopes.Framework.Infof("KUBECONFIG: %s", value)
	out, err := parseKubeConfigs(value, string(filepath.ListSeparator))
	if err != nil {
		panic(err)
	}
	if len(out) == 0 {
		out = []string{defaultKubeConfig}
	}
	scopes.Framework.Infof("Using KUBECONFIG array: %v", out)
	return out
}

func parseKubeConfigs(value, separator string) ([]string, error) {
	if len(value) == 0 {
		return make([]string, 0), nil
	}

	parts := strings.Split(value, separator)
	out := make([]string, 0, len(parts))
	for _, f := range parts {
		if f != "" {
			if err := normalizeFile(&f); err != nil {
				return nil, err
			}
			out = append(out, f)
		}
	}
	return out, nil
}

func newControlPlaneTopology(kubeConfigs []string) (clusterTopology, error) {
	topology, err := parseControlPlaneTopology()
	if err != nil {
		return nil, err
	}

	if len(topology) == 0 {
		// Default to deploying a control plane per cluster.
		for index := range kubeConfigs {
			topology[resource.ClusterIndex(index)] = resource.ClusterIndex(index)
		}
		return topology, nil
	}

	// Verify that all of the specified clusters are valid.
	numClusters := len(kubeConfigs)
	for cIndex, cpIndex := range topology {
		if int(cIndex) >= numClusters {
			return nil, fmt.Errorf("failed parsing control plane topology: cluster index %d "+
				"exceeds number of available clusters %d", cIndex, numClusters)
		}
		if int(cpIndex) >= numClusters {
			return nil, fmt.Errorf("failed parsing control plane topology: control plane cluster index %d "+""+
				"exceeds number of available clusters %d", cpIndex, numClusters)
		}
	}
	return topology, nil
}

func parseControlPlaneTopology() (clusterTopology, error) {
	out := make(clusterTopology)
	if controlPlaneTopology == "" {
		return out, nil
	}

	values := strings.Split(controlPlaneTopology, ",")
	for _, v := range values {
		parts := strings.Split(v, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("failed parsing control plane mapping entry %s", v)
		}
		clusterIndex, err := strconv.Atoi(parts[0])
		if err != nil || clusterIndex < 0 {
			return nil, fmt.Errorf("failed parsing control plane mapping entry %s: failed parsing cluster index", v)
		}
		controlPlaneClusterIndex, err := strconv.Atoi(parts[1])
		if err != nil || clusterIndex < 0 {
			return nil, fmt.Errorf("failed parsing control plane mapping entry %s: failed parsing control plane index", v)
		}
		out[resource.ClusterIndex(clusterIndex)] = resource.ClusterIndex(controlPlaneClusterIndex)
	}
	return out, nil
}

func newConfigTopology(kubeConfigs []string, fallback clusterTopology) (clusterTopology, error) {
	topology, err := parseConfigTopology()
	if err != nil {
		return nil, err
	}

	if len(topology) == 0 {
		// Default to every cluster using config from it's control plane cluster.
		for k, v := range fallback {
			topology[k] = v
		}
		return topology, nil
	}

	// Verify that all of the specified clusters are valid.
	numClusters := len(kubeConfigs)
	for cIndex, cfIndex := range topology {
		if int(cIndex) >= numClusters {
			return nil, fmt.Errorf("failed parsing config topology: cluster index %d "+
				"exceeds number of available clusters %d", cIndex, numClusters)
		}
		if int(cfIndex) >= numClusters {
			return nil, fmt.Errorf("failed parsing config topology: config cluster index %d "+""+
				"exceeds number of available clusters %d", cfIndex, numClusters)
		}
	}
	return topology, nil
}

func parseConfigTopology() (clusterTopology, error) {
	out := make(clusterTopology)
	if configTopology == "" {
		return out, nil
	}

	values := strings.Split(configTopology, ",")
	for _, v := range values {
		parts := strings.Split(v, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("failed parsing config mapping entry %s", v)
		}
		clusterIndex, err := strconv.Atoi(parts[0])
		if err != nil || clusterIndex < 0 {
			return nil, fmt.Errorf("failed parsing config mapping entry %s: failed parsing cluster index", v)
		}
		configClusterIndex, err := strconv.Atoi(parts[1])
		if err != nil || clusterIndex < 0 {
			return nil, fmt.Errorf("failed parsing config mapping entry %s: failed parsing config cluster index", v)
		}
		out[resource.ClusterIndex(clusterIndex)] = resource.ClusterIndex(configClusterIndex)
	}
	return out, nil
}

func parseNetworkTopology(kubeConfigs []string) (map[resource.ClusterIndex]string, error) {
	out := make(map[resource.ClusterIndex]string)
	if controlPlaneTopology == "" {
		for index := range kubeConfigs {
			out[resource.ClusterIndex(index)] = "network-0"
		}
		return out, nil
	}
	numClusters := len(kubeConfigs)
	values := strings.Split(networkTopology, ",")
	for _, v := range values {
		parts := strings.Split(v, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("failed parsing network mapping mapping entry %s", v)
		}
		clusterIndex, err := strconv.Atoi(parts[0])
		if err != nil || clusterIndex < 0 {
			return nil, fmt.Errorf("failed parsing network mapping entry %s: failed parsing cluster index", v)
		}
		if clusterIndex >= numClusters {
			return nil, fmt.Errorf("failed parsing network topology: cluster index: %d "+
				"exceeds number of available clusters %d", clusterIndex, numClusters)
		}
		if len(parts[1]) == 0 {
			return nil, fmt.Errorf("failed parsing network mapping entry %s: failed parsing network name", v)
		}
		out[resource.ClusterIndex(clusterIndex)] = parts[1]
	}
	return out, nil
}

func normalizeFile(path *string) error {
	// trim leading/trailing spaces from the path and if it uses the homedir ~, expand it.
	var err error
	*path = strings.TrimSpace(*path)
	*path, err = homedir.Expand(*path)
	if err != nil {
		return err
	}

	return nil
}

// init registers the command-line flags that we can exposed for "go test".
func init() {
	flag.StringVar(&kubeConfigs, "istio.test.kube.config", "",
		"A comma-separated list of paths to kube config files for cluster environments.")
	flag.BoolVar(&settingsFromCommandLine.Minikube, "istio.test.kube.minikube", settingsFromCommandLine.Minikube,
		"Deprecated. See istio.test.kube.loadbalancer. Setting this flag will fail tests.")
	flag.BoolVar(&settingsFromCommandLine.LoadBalancerSupported, "istio.test.kube.loadbalancer", settingsFromCommandLine.LoadBalancerSupported,
		"Indicates whether or not clusters in the environment support external IPs for LoadBalaner services. Used "+
			"to obtain the right IP address for the Ingress Gateway. Set --istio.test.kube.loadbalancer=false for local KinD/Minikube tests."+
			"without MetalLB installed.")
	flag.StringVar(&controlPlaneTopology, "istio.test.kube.controlPlaneTopology",
		"", "Specifies the mapping for each cluster to the cluster hosting its control plane. The value is a "+
			"comma-separated list of the form <clusterIndex>:<controlPlaneClusterIndex>, where the indexes refer to the order in which "+
			"a given cluster appears in the 'istio.test.kube.config' flag. This topology also determines where control planes should "+
			"be deployed. If not specified, the default is to deploy a control plane per cluster (i.e. `replicated control "+
			"planes') and map every cluster to itself (e.g. 0:0,1:1,...).")
	flag.StringVar(&networkTopology, "istio.test.kube.networkTopology",
		"", "Specifies the mapping for each cluster to it's network name, for multi-network scenarios. The value is a "+
			"comma-separated list of the form <clusterIndex>:<networkName>, where the indexes refer to the order in which "+
			"a given cluster appears in the 'istio.test.kube.config' flag. If not specified, network name will be left unset")
	flag.StringVar(&configTopology, "istio.test.kube.configTopology",
		"", "Specifies the mapping for each cluster to the cluster hosting its config. The value is a "+
			"comma-separated list of the form <clusterIndex>:<configClusterIndex>, where the indexes refer to the order in which "+
			"a given cluster appears in the 'istio.test.kube.config' flag. If not specified, the default is every cluster maps to itself(e.g. 0:0,1:1,...).")
}
