/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package create

import (
	"fmt"
	"strings"
	"testing"

	"sigs.k8s.io/kind/pkg/cluster/internal/providers"
	"sigs.k8s.io/kind/pkg/cluster/nodes"
	"sigs.k8s.io/kind/pkg/internal/apis/config"
	"sigs.k8s.io/kind/pkg/internal/cli"
	"sigs.k8s.io/kind/pkg/log"
)

// fakeProvider records whether Provision was reached. Validation must abort
// before Provision on hard errors, so no container runtime is ever needed.
type fakeProvider struct {
	provisionCalled bool
}

func (f *fakeProvider) Provision(_ *cli.Status, _ *config.Cluster) error {
	f.provisionCalled = true
	return nil
}

func (f *fakeProvider) ListClusters() ([]string, error)                     { return nil, nil }
func (f *fakeProvider) ListNodes(string) ([]nodes.Node, error)              { return nil, nil }
func (f *fakeProvider) DeleteNodes([]nodes.Node) error                      { return nil }
func (f *fakeProvider) GetAPIServerEndpoint(string) (string, error)         { return "", nil }
func (f *fakeProvider) GetAPIServerInternalEndpoint(string) (string, error) { return "", nil }
func (f *fakeProvider) CollectLogs(string, []nodes.Node) error              { return nil }
func (f *fakeProvider) Info() (*providers.ProviderInfo, error) {
	return &providers.ProviderInfo{Cgroup2: true}, nil
}

// warningLogger captures warnings so the test can assert they reach CI logs.
type warningLogger struct {
	log.NoopLogger
	warnings []string
}

func (l *warningLogger) Warnf(format string, args ...interface{}) {
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
}

func TestClusterAbortsBeforeProvisionOnHardError(t *testing.T) {
	t.Parallel()
	// two nodes claiming the same fixed host port: a hard, static conflict
	cluster := &config.Cluster{
		Name: "kind",
		Nodes: []config.Node{
			{
				Role: config.ControlPlaneRole, Image: "image:latest",
				ExtraPortMappings: []config.PortMapping{
					{ContainerPort: 80, HostPort: 8443, ListenAddress: "0.0.0.0", Protocol: config.PortMappingProtocolTCP},
				},
			},
			{
				Role: config.WorkerRole, Image: "image:latest",
				ExtraPortMappings: []config.PortMapping{
					{ContainerPort: 80, HostPort: 8443, ListenAddress: "0.0.0.0", Protocol: config.PortMappingProtocolTCP},
				},
			},
		},
	}
	config.SetDefaultsCluster(cluster)

	provider := &fakeProvider{}
	err := Cluster(log.NoopLogger{}, provider, &ClusterOptions{Config: cluster})
	if err == nil {
		t.Fatal("a hard port conflict must fail cluster creation")
	}
	if provider.provisionCalled {
		t.Fatal("Provision must never be reached when static validation fails")
	}
}

func TestClusterLogsWarningsButProceeds(t *testing.T) {
	t.Parallel()
	// an even control-plane count is only a warning; reaching Provision proves
	// the warning did not block creation. The fake returns a nil error and the
	// later creation steps stop before touching a real runtime.
	cluster := &config.Cluster{
		Name: "kind",
		Nodes: []config.Node{
			{Role: config.ControlPlaneRole, Image: "image:latest"},
			{Role: config.ControlPlaneRole, Image: "image:latest"},
		},
	}
	config.SetDefaultsCluster(cluster)

	logger := &warningLogger{}
	provider := &fakeProvider{}
	_ = Cluster(logger, provider, &ClusterOptions{
		Config:                        cluster,
		StopBeforeSettingUpKubernetes: true,
	})

	if !provider.provisionCalled {
		t.Fatal("warnings must not prevent Provision from being reached")
	}
	found := false
	for _, w := range logger.warnings {
		if strings.Contains(w, "control-plane") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the even control-plane warning must be logged, got %v", logger.warnings)
	}
}
