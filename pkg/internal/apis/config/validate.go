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

package config

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"sigs.k8s.io/kind/pkg/errors"
)

// similar to valid docker container names, but since we will prefix
// and suffix this name, we can relax it a little
// see NewContext() for usage
// https://godoc.org/github.com/docker/docker/daemon/names#pkg-constants
var validNameRE = regexp.MustCompile(`^[a-z0-9.-]+$`)

// Validate returns an aggregate containing one error per hard error in the
// configuration, or nil if there are none. Warnings are not part of this
// error based API; callers that need warnings or structured diagnostics should
// use ValidateCluster instead.
//
// The configuration must have been defaulted (SetDefaultsCluster) before
// validation, matching the creation flow ordering.
func (c *Cluster) Validate() error {
	return ValidateCluster(c, nil).HardErrorsAggregate()
}

// Validate returns an aggregate of the hard errors specific to this node,
// or nil if there are none. It runs the cluster independent per-node rules
// (role, image, ports, mounts) over a single-node cluster so that no cluster
// level diagnostics (E.G. control-plane composition) are included.
func (n *Node) Validate() error {
	single := &Cluster{Nodes: []Node{*n}}
	// a standalone node cannot carry cluster networking defaults; give it
	// the IPv4 default so listen address normalization matches creation
	single.Networking.IPFamily = IPv4Family
	result := ValidateCluster(single, nil)
	errs := make([]error, 0)
	for _, d := range result.Diagnostics {
		if d.Severity == SeverityError && d.NodeIndex == 0 {
			errs = append(errs, errors.New(d.Message))
		}
	}
	return errors.NewAggregate(errs)
}

func validatePort(port int32) error {
	// NOTE: -1 is a special value for auto-selecting the port in the container
	// backend where possible as opposed to in kind itself.
	if port < -1 || port > 65535 {
		return errors.Errorf("invalid port number: %d", port)
	}
	return nil
}

func validateSubnets(subnetStr string, ipFamily ClusterIPFamily) error {
	allErrs := []error{}

	cidrsString := strings.Split(subnetStr, ",")
	subnets := make([]*net.IPNet, 0, len(cidrsString))
	for _, cidrString := range cidrsString {
		_, cidr, err := net.ParseCIDR(cidrString)
		if err != nil {
			return fmt.Errorf("failed to parse cidr value:%q with error: %v", cidrString, err)
		}
		subnets = append(subnets, cidr)
	}

	dualstack := ipFamily == DualStackFamily
	switch {
	// if no subnets are defined
	case len(subnets) == 0:
		allErrs = append(allErrs, errors.New("no subnets defined"))
	// if DualStack only 2 CIDRs allowed
	case dualstack && len(subnets) > 2:
		allErrs = append(allErrs, errors.New("expected one (IPv4 or IPv6) CIDR or two CIDRs from each family for dual-stack networking"))
	// if DualStack and there are 2 CIDRs validate if there is at least one of each IP family
	case dualstack && len(subnets) == 2:
		areDualStackCIDRs, err := isDualStackCIDRs(subnets)
		if err != nil {
			allErrs = append(allErrs, err)
		} else if !areDualStackCIDRs {
			allErrs = append(allErrs, errors.New("expected one (IPv4 or IPv6) CIDR or two CIDRs from each family for dual-stack networking"))
		}
	// if not DualStack only one CIDR allowed
	case !dualstack && len(subnets) > 1:
		allErrs = append(allErrs, errors.New("only one CIDR allowed for single-stack networking"))
	case ipFamily == IPv4Family && subnets[0].IP.To4() == nil:
		allErrs = append(allErrs, errors.New("expected IPv4 CIDR for IPv4 family"))
	case ipFamily == IPv6Family && subnets[0].IP.To4() != nil:
		allErrs = append(allErrs, errors.New("expected IPv6 CIDR for IPv6 family"))
	}

	if len(allErrs) > 0 {
		return errors.NewAggregate(allErrs)
	}
	return nil
}

// isDualStackCIDRs returns if
// - all are valid cidrs
// - at least one cidr from each family (v4 or v6)
func isDualStackCIDRs(cidrs []*net.IPNet) (bool, error) {
	v4Found := false
	v6Found := false
	for _, cidr := range cidrs {
		if cidr == nil {
			return false, fmt.Errorf("cidr %v is invalid", cidr)
		}

		if v4Found && v6Found {
			continue
		}

		if cidr.IP != nil && cidr.IP.To4() == nil {
			v6Found = true
			continue
		}
		v4Found = true
	}

	return v4Found && v6Found, nil
}
