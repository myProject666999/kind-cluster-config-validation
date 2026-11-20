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

import "fmt"

// validateRoles checks node role values and the control plane composition.
//
//   - an unknown role string is a hard error.
//   - at least one control-plane node is required; a control-plane-only
//     cluster is legal.
//   - a positive even number of control-plane nodes produces a warning only.
//     An even etcd voting membership cannot form a quorum when one member is
//     down. Creation still succeeds because kind provisions an implicit
//     external load balancer for every multi control-plane cluster (see
//     ClusterHasImplicitLoadBalancer), so the warning is advisory and stays
//     consistent with that HA behavior.
func (v *validator) validateRoles() {
	controlPlanes := 0
	for ni := range v.cluster.Nodes {
		node := &v.cluster.Nodes[ni]
		roleField := nodeField(ni, "role")
		switch node.Role {
		case ControlPlaneRole:
			controlPlanes++
		case WorkerRole:
		default:
			v.addError(
				codeInvalidRole, ni, roleField, v.origin(roleField),
				fmt.Sprintf("%q is not a valid node role", node.Role),
			)
		}
	}

	const nodesField = "nodes"
	if controlPlanes < 1 {
		v.addError(
			codeMissingControlPlane, -1, nodesField, v.origin(nodesField),
			fmt.Sprintf("must have at least one %s node", string(ControlPlaneRole)),
		)
		return
	}
	if controlPlanes%2 == 0 {
		v.addWarning(
			codeEvenControlPlaneCount, -1, nodesField, v.origin(nodesField),
			fmt.Sprintf(
				"cluster has %d control-plane nodes; an even control-plane count cannot form an etcd quorum when one node is down, prefer an odd count",
				controlPlanes,
			),
		)
	}
}
