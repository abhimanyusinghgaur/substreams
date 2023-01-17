// Copyright 2021 dfuse Platform Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/streamingfast/cli"
)

var Cmd = &cobra.Command{Use: "tools", Short: "Developer tools related to substreams"}

var Example = func(in string) string {
	return string(cli.Example(in))
}

var ExamplePrefixed = func(prefix, in string) string {
	return string(cli.ExamplePrefixed(prefix, in))
}

func mustGetString(cmd *cobra.Command, flagName string) string {
	val, err := cmd.Flags().GetString(flagName)
	if err != nil {
		panic(fmt.Sprintf("flags: couldn't find flag %q", flagName))
	}
	return val
}
func mustGetInt64(cmd *cobra.Command, flagName string) int64 {
	val, err := cmd.Flags().GetInt64(flagName)
	if err != nil {
		panic(fmt.Sprintf("flags: couldn't find flag %q", flagName))
	}
	return val
}
func mustGetUint64(cmd *cobra.Command, flagName string) uint64 {
	val, err := cmd.Flags().GetUint64(flagName)
	if err != nil {
		panic(fmt.Sprintf("flags: couldn't find flag %q", flagName))
	}
	return val
}
func mustGetBool(cmd *cobra.Command, flagName string) bool {
	val, err := cmd.Flags().GetBool(flagName)
	if err != nil {
		panic(fmt.Sprintf("flags: couldn't find flag %q", flagName))
	}
	return val
}
func mustGetDuration(cmd *cobra.Command, flagName string) time.Duration {
	val, err := cmd.Flags().GetDuration(flagName)
	if err != nil {
		panic(fmt.Sprintf("flags: couldn't find flag %q", flagName))
	}
	return val
}

func ResolveManifestFile(input string) (manifestName string, err error) {
	if input == "" {
		if _, err := os.Stat("substreams.yaml"); err == nil {
			return "substreams.yaml", nil
		} else if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("finding manifest file: %w", err)
		}
	} else {

		// Check valid file first
		if _, err := os.Stat(input); err == nil {
			_, err := os.Open(input)
			if err == nil {
				return input, nil
			}
			// Check if dir w/ manifest
		} else if errors.Is(err, os.ErrNotExist) {
			potentialManifestPath := filepath.Join(input, "substreams.yaml")
			if _, err := os.Stat(potentialManifestPath); err == nil {
				return potentialManifestPath, nil
			}
			return "", fmt.Errorf("finding manifest file: %w", err)
		}
	}
	return "", nil
}
