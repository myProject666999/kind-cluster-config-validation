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
	"fmt"
	"os"

	yaml "go.yaml.in/yaml/v3"

	"sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/internal/apis/config"
)

// ParseWithProvenance behaves like Parse but additionally records which
// field paths were physically present in the source YAML. The provenance
// sidecar lets diagnostics distinguish explicitly written values from
// statically defaulted ones; it must be built before defaulting runs,
// because defaulting erases the distinction.
//
// The strict decoding behavior (rejecting unknown fields) is unchanged.
func ParseWithProvenance(raw []byte) (*config.Cluster, *config.Provenance, error) {
	// get kind & apiVersion
	tm := typeMeta{}
	if err := yaml.Unmarshal(raw, &tm); err != nil {
		return nil, nil, errors.Wrap(err, "could not determine kind / apiVersion for config")
	}

	// decode specific (apiVersion, kind)
	switch tm.APIVersion {
	// handle v1alpha4
	case "kind.x-k8s.io/v1alpha4":
		if tm.Kind != "Cluster" {
			return nil, nil, errors.Errorf("unknown kind %s for apiVersion: %s", tm.Kind, tm.APIVersion)
		}
		// load version
		cfg := &v1alpha4.Cluster{}
		if err := yamlUnmarshalStrict(raw, cfg); err != nil {
			return nil, nil, errors.Wrap(err, "unable to decode config")
		}
		provenance, err := provenanceFromYAML(raw)
		if err != nil {
			return nil, nil, errors.Wrap(err, "unable to decode config")
		}
		// apply defaults for version and convert
		return V1Alpha4ToInternal(cfg), provenance, nil
	}

	// unknown apiVersion if we haven't already returned ...
	return nil, nil, errors.Errorf("unknown apiVersion: %s", tm.APIVersion)
}

// LoadWithProvenance behaves like Load but also returns the provenance
// sidecar. The default configuration (empty path) has no source, so its
// provenance is nil and every field resolves to origin unknown.
func LoadWithProvenance(path string) (*config.Cluster, *config.Provenance, error) {
	// special case: empty path -> default config
	// TODO(bentheelder): consider removing this
	if path == "" {
		out := &config.Cluster{}
		config.SetDefaultsCluster(out)
		return out, nil, nil
	}

	// read in file
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, errors.Wrap(err, "error reading file")
	}

	return ParseWithProvenance(raw)
}

// provenanceFromYAML decodes the raw YAML into a node tree and collects the
// path of every field that is physically present. The path grammar matches
// the diagnostic field paths exactly:
//
//	nodes[0].extraPortMappings[1].hostPort
//	nodes[2].role
//	networking.apiServerPort
//
// YAML field names for the kind configuration types are identical to the
// internal lower-camel Go field names, so a generic structural walk produces
// the same grammar used by the validators.
func provenanceFromYAML(raw []byte) (*config.Provenance, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	presence := config.FieldPresence{}
	collectFieldPresence(&doc, "", presence)
	return config.NewProvenance(presence), nil
}

// collectFieldPresence walks a yaml.Node tree and records the path of every
// mapping field. The walk is purely structural and therefore deterministic.
func collectFieldPresence(node *yaml.Node, prefix string, out config.FieldPresence) {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			collectFieldPresence(child, prefix, out)
		}
	case yaml.MappingNode:
		// Content alternates key nodes and value nodes
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			path := key.Value
			if prefix != "" {
				path = prefix + "." + key.Value
			}
			out[path] = struct{}{}
			collectFieldPresence(value, path, out)
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			collectFieldPresence(child, fmt.Sprintf("%s[%d]", prefix, i), out)
		}
	}
}
