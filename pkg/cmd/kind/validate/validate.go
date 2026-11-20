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

// Package validate implements the `kind validate` commands
package validate

import (
	"errors"

	"github.com/spf13/cobra"

	"sigs.k8s.io/kind/pkg/cmd"
	validateconfig "sigs.k8s.io/kind/pkg/cmd/kind/validate/config"
	"sigs.k8s.io/kind/pkg/log"
)

// NewCommand returns a new cobra.Command for configuration validation
func NewCommand(logger log.Logger, streams cmd.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validates one of [config]",
		Long:  "Validates kind configuration without creating a cluster (config)",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := cmd.Help()
			if err != nil {
				return err
			}
			return errors.New("Subcommand is required")
		},
	}
	cmd.AddCommand(validateconfig.NewCommand(logger, streams))
	return cmd
}
