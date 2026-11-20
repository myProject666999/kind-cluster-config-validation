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

// Package config implements the `kind validate config` command
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"sigs.k8s.io/kind/pkg/cmd"
	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/internal/apis/config"
	"sigs.k8s.io/kind/pkg/internal/apis/config/encoding"
	"sigs.k8s.io/kind/pkg/log"
)

type flagpole struct {
	Config         string
	CheckHostPaths bool
	Output         string
}

// output formats
const (
	outputText = "text"
	outputJSON = "json"
)

// NewCommand returns a new cobra.Command for validating a kind config file
// without creating any cluster or contacting a container runtime.
func NewCommand(logger log.Logger, streams cmd.IOStreams) *cobra.Command {
	flags := &flagpole{}
	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		Use:   "config",
		Short: "Validates a kind cluster configuration",
		Long: "Validates a kind cluster configuration without creating a cluster.\n\n" +
			"Static validation never contacts the container runtime, host network or\n" +
			"filesystem and is deterministic. The optional host path precheck is\n" +
			"explicitly enabled and reports environment dependent findings separately.\n\n" +
			"Exits non-zero when hard errors are found; warnings alone do not fail.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runE(logger, streams, flags)
		},
	}
	cmd.Flags().StringVar(
		&flags.Config,
		"config",
		"",
		"path to a kind config file, or - to read from stdin",
	)
	cmd.Flags().BoolVar(
		&flags.CheckHostPaths,
		"check-host-paths",
		false,
		"also inspect local host paths for existence, type and permissions (environment dependent)",
	)
	cmd.Flags().StringVar(
		&flags.Output,
		"output",
		outputText,
		"output format, one of [text, json]",
	)
	return cmd
}

func runE(logger log.Logger, streams cmd.IOStreams, flags *flagpole) error {
	if flags.Output != outputText && flags.Output != outputJSON {
		return errors.Errorf("invalid --output %q: must be %q or %q", flags.Output, outputText, outputJSON)
	}

	raw, err := readConfig(flags.Config, streams.In)
	if err != nil {
		return err
	}

	// parse + default, retaining source provenance; no runtime is involved
	cluster, provenance, err := encoding.ParseWithProvenance(raw)
	if err != nil {
		return errors.Wrap(err, "failed to parse config")
	}
	// apply the internal defaults as well, matching the creation flow's
	// fixupOptions so validation sees the same final configuration
	config.SetDefaultsCluster(cluster)

	// phase A: pure, deterministic static validation
	result := config.ValidateCluster(cluster, provenance)

	// phase B: optional, environment dependent local precheck, kept separate
	var hostPathResult *config.HostPathResult
	if flags.CheckHostPaths {
		hostPathResult = config.CheckHostPaths(cluster, config.OSHostPathInspector{})
	}

	if flags.Output == outputJSON {
		if err := writeJSON(streams.Out, result.Render(), hostPathResult); err != nil {
			return err
		}
	} else {
		writeText(streams.Out, result.Render(), hostPathResult)
	}

	// warnings alone are a successful validation; hard errors fail the command
	// based on the full, untruncated result
	if result.HasErrors() {
		return errors.Errorf("configuration is invalid: %d error(s), %d warning(s)",
			result.ErrorCount, result.WarningCount)
	}
	logger.V(0).Infof("configuration is valid (%d warning(s))", result.WarningCount)
	return nil
}

// readConfig loads the raw YAML bytes from the given file path or stdin.
// An empty path means the built-in default configuration.
func readConfig(path string, stdin io.Reader) ([]byte, error) {
	if path == "" {
		return nil, errors.New("--config is required (use a file path or - for stdin)")
	}
	if path != "-" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.Wrap(err, "error reading config file")
		}
		return raw, nil
	}
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return nil, errors.Wrap(err, "error reading config from stdin")
	}
	return raw, nil
}

func writeJSON(out io.Writer, result *config.Result, hostPathResult *config.HostPathResult) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if hostPathResult == nil {
		return encoder.Encode(result)
	}
	// render the two phases as distinct keys so environment dependent data can
	// never be confused with static diagnostics
	payload := struct {
		*config.Result
		HostPathFindings []config.HostPathFinding `json:"hostPathFindings"`
	}{
		Result:           result,
		HostPathFindings: hostPathResult.Findings,
	}
	return encoder.Encode(payload)
}

func writeText(out io.Writer, result *config.Result, hostPathResult *config.HostPathResult) {
	for _, d := range result.Diagnostics {
		location := d.Field
		if d.NodeIndex >= 0 && !strings.HasPrefix(location, "nodes[") {
			location = fmt.Sprintf("nodes[%d].%s", d.NodeIndex, location)
		}
		if d.SecondField != "" {
			fmt.Fprintf(out, "%s: %s (%s, conflicts with %s)\n", d.Severity, d.Message, location, d.SecondField)
		} else {
			fmt.Fprintf(out, "%s: %s (%s, origin=%s)\n", d.Severity, d.Message, location, d.Origin)
		}
	}
	if hostPathResult != nil {
		for _, finding := range hostPathResult.Findings {
			fmt.Fprintf(out, "host-path: %s: %s (%s)\n", finding.Severity, finding.Message, finding.Field)
		}
	}
	fmt.Fprintf(out, "summary: %d error(s), %d warning(s), %d total diagnostic(s)\n",
		result.ErrorCount, result.WarningCount, result.Total)
}
