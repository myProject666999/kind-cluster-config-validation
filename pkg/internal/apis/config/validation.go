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

package config

import (
	"sigs.k8s.io/kind/pkg/errors"
)

// validator runs the pure, static validation rules against a defaulted
// cluster configuration. It never inspects the runtime, the host filesystem
// or host network state, so validating the same configuration twice yields
// byte-identical results.
type validator struct {
	cluster    *Cluster
	provenance *Provenance
	diags      []Diagnostic
}

// ValidateCluster performs the full static validation of an already
// defaulted cluster. provenance may be nil, in which case every field's
// origin is reported as unknown. The cluster is never mutated.
//
// The returned Result always contains the complete, deterministically ordered
// list of diagnostics and authoritative counts; use Result.Render for a
// bounded, truncation-aware view.
func ValidateCluster(c *Cluster, provenance *Provenance) *Result {
	v := &validator{cluster: c, provenance: provenance}
	v.validateClusterScoped()
	v.validateRoles()
	for ni := range c.Nodes {
		v.validateNode(ni)
	}
	v.validateMounts()
	v.validatePortMappings()
	return (&Result{Diagnostics: v.diags}).finalize()
}

// validateClusterScoped holds the historical cluster level checks from
// Cluster.Validate: name, api server port, IP family, subnets and proxy mode.
func (v *validator) validateClusterScoped() {
	if !validNameRE.MatchString(v.cluster.Name) {
		const field = "name"
		v.addError(
			codeInvalidName, -1, field, v.origin(field),
			errors.Errorf("'%s' is not a valid cluster name, cluster names must match `%s`",
				v.cluster.Name, validNameRE.String()).Error(),
		)
	}

	// the api server port only needs checking if we aren't picking a random one
	// at runtime
	if v.cluster.Networking.APIServerPort != 0 {
		const field = "networking.apiServerPort"
		if err := validatePort(v.cluster.Networking.APIServerPort); err != nil {
			v.addError(codeInvalidAPIServerPort, -1, field, v.origin(field), err.Error())
		}
	}

	const ipFamilyField = "networking.ipFamily"
	switch v.cluster.Networking.IPFamily {
	case IPv4Family, IPv6Family, DualStackFamily:
	default:
		v.addError(
			codeInvalidIPFamily, -1, ipFamilyField, v.origin(ipFamilyField),
			errors.Errorf("invalid ipFamily: %s", v.cluster.Networking.IPFamily).Error(),
		)
	}

	const podSubnetField = "networking.podSubnet"
	if err := validateSubnets(v.cluster.Networking.PodSubnet, v.cluster.Networking.IPFamily); err != nil {
		v.addError(codeInvalidSubnet, -1, podSubnetField, v.origin(podSubnetField),
			errors.Errorf("invalid pod subnet %v", err).Error())
	}

	const serviceSubnetField = "networking.serviceSubnet"
	if err := validateSubnets(v.cluster.Networking.ServiceSubnet, v.cluster.Networking.IPFamily); err != nil {
		v.addError(codeInvalidSubnet, -1, serviceSubnetField, v.origin(serviceSubnetField),
			errors.Errorf("invalid service subnet %v", err).Error())
	}

	const kubeProxyModeField = "networking.kubeProxyMode"
	switch v.cluster.Networking.KubeProxyMode {
	case IPTablesProxyMode, IPVSProxyMode, NoneProxyMode, NFTablesProxyMode:
	default:
		v.addError(
			codeInvalidKubeProxyMode, -1, kubeProxyModeField, v.origin(kubeProxyModeField),
			errors.Errorf("invalid kubeProxyMode: %s", v.cluster.Networking.KubeProxyMode).Error(),
		)
	}
}

// validateNode holds the historical per-node checks that are not covered by
// the dedicated mount, port and role validators.
func (v *validator) validateNode(ni int) {
	node := &v.cluster.Nodes[ni]

	const imageField = "image"
	if node.Image == "" {
		imagePath := nodeField(ni, imageField)
		v.addError(codeImageRequired, ni, imagePath, v.origin(imagePath), "image is a required field")
	}
}

func (v *validator) origin(field string) Origin {
	return v.provenance.Origin(field)
}

func (v *validator) addError(code string, nodeIndex int, field string, origin Origin, message string) {
	v.diags = append(v.diags, Diagnostic{
		Severity:  SeverityError,
		Code:      code,
		Message:   message,
		NodeIndex: nodeIndex,
		Field:     field,
		Origin:    origin,
	})
}

func (v *validator) addWarning(code string, nodeIndex int, field string, origin Origin, message string) {
	v.diags = append(v.diags, Diagnostic{
		Severity:  SeverityWarning,
		Code:      code,
		Message:   message,
		NodeIndex: nodeIndex,
		Field:     field,
		Origin:    origin,
	})
}
