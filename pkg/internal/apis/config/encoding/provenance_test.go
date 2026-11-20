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

package encoding

import (
	"testing"

	"sigs.k8s.io/kind/pkg/internal/apis/config"
)

const provenanceYAML = `
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: worker
  extraPortMappings:
  - containerPort: 80
    hostPort: 8080
    listenAddress: 127.0.0.1
  extraMounts:
  - hostPath: /host
    containerPath: /data
- role: control-plane
`

func TestParseWithProvenance(t *testing.T) {
	t.Parallel()
	cluster, provenance, err := ParseWithProvenance([]byte(provenanceYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provenance == nil {
		t.Fatal("expected non-nil provenance")
	}

	explicitFields := []string{
		"nodes",
		"nodes[0].role",
		"nodes[0].extraPortMappings[0].containerPort",
		"nodes[0].extraPortMappings[0].hostPort",
		"nodes[0].extraPortMappings[0].listenAddress",
		"nodes[0].extraMounts[0].hostPath",
		"nodes[0].extraMounts[0].containerPath",
		"nodes[1].role",
	}
	for _, field := range explicitFields {
		if provenance.Origin(field) != config.OriginExplicit {
			t.Errorf("expected %s to be explicit, got %s", field, provenance.Origin(field))
		}
	}

	// omitted fields receive static defaults and must read as defaulted
	defaultedFields := []string{
		"nodes[0].image",
		"networking.ipFamily",
		"nodes[0].extraPortMappings[0].protocol",
	}
	for _, field := range defaultedFields {
		if provenance.Origin(field) != config.OriginDefaulted {
			t.Errorf("expected %s to be defaulted, got %s", field, provenance.Origin(field))
		}
	}

	// the cluster itself must be fully defaulted and usable
	if cluster.Networking.IPFamily != config.IPv4Family {
		t.Errorf("expected defaulted ipv4 family, got %q", cluster.Networking.IPFamily)
	}
	if cluster.Nodes[0].Image == "" || cluster.Nodes[0].Role != config.WorkerRole {
		t.Errorf("node was not defaulted: %+v", cluster.Nodes[0])
	}
}

func TestParseWithoutProvenanceReportsUnknownOrigins(t *testing.T) {
	t.Parallel()
	cluster, err := Parse([]byte(provenanceYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// exercising the validator through the plain Parse path: the nil
	// provenance must degrade to unknown origins rather than panic
	result := config.ValidateCluster(cluster, nil)
	for _, d := range result.Diagnostics {
		if d.Origin != config.OriginUnknown {
			t.Fatalf("nil provenance must yield unknown origin, got %+v", d)
		}
	}
}

func TestLoadWithProvenanceDefaultConfigHasNoSource(t *testing.T) {
	t.Parallel()
	cluster, provenance, err := LoadWithProvenance("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provenance != nil {
		t.Fatal("the default configuration has no source, provenance must be nil")
	}
	if len(cluster.Nodes) != 1 || cluster.Nodes[0].Role != config.ControlPlaneRole {
		t.Fatalf("unexpected default cluster: %+v", cluster)
	}
}

func TestParseWithProvenanceRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	raw := []byte(`
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
bogusField: true
`)
	if _, _, err := ParseWithProvenance(raw); err == nil {
		t.Fatal("strict decoding must still reject unknown fields")
	}
}
