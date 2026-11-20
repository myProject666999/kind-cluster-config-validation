/*
Copyright 2018 The Kubernetes Authors.

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
	"bytes"

	yaml "go.yaml.in/yaml/v3"

	"sigs.k8s.io/kind/pkg/internal/apis/config"
)

// Load reads the file at path and attempts to convert into a `kind` Config; the file
// can be one of the different API versions defined in scheme.
// If path == "" then the default config is returned
// If path == "-" then reads from stdin
func Load(path string) (*config.Cluster, error) {
	cluster, _, err := LoadWithProvenance(path)
	return cluster, err
}

// Parse parses a cluster config from raw (yaml) bytes
// It will always return the current internal version after defaulting and
// conversion from the read version
func Parse(raw []byte) (*config.Cluster, error) {
	cluster, _, err := ParseWithProvenance(raw)
	return cluster, err
}

// basically metav1.TypeMeta, but with yaml tags
type typeMeta struct {
	Kind       string `yaml:"kind,omitempty"`
	APIVersion string `yaml:"apiVersion,omitempty"`
}

func yamlUnmarshalStrict(raw []byte, v interface{}) error {
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	return d.Decode(v)
}
