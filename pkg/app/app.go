/*
Copyright 2017 The Kubernetes Authors All rights reserved.

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

package app

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"os"

	"github.com/kubernetes/kompose/pkg/kobject"
	"github.com/kubernetes/kompose/pkg/loader"
	"github.com/kubernetes/kompose/pkg/transformer"
	"github.com/kubernetes/kompose/pkg/transformer/kubernetes"
	"github.com/kubernetes/kompose/pkg/transformer/openshift"
)

var (
	// DefaultComposeFiles is a list of filenames that kompose will use if no file is explicitly set
	DefaultComposeFiles = []string{
		"docker-compose.yml",
		"docker-compose.yaml",
		"container-compose.yml",
		"container-compose.yaml",
	}
)

const (
	// ProviderKubernetes is provider kubernetes
	ProviderKubernetes = "kubernetes"
	// ProviderOpenshift is provider openshift
	ProviderOpenshift = "openshift"
	// DefaultProvider - provider that will be used if there is no provider was explicitly set
	DefaultProvider = ProviderKubernetes
)

var inputFormat = "compose"

// ValidateFlags validates all command line flags
func ValidateFlags(args []string, cmd *cobra.Command, opt *kobject.ConvertOptions) error {
	if opt.OutFile == "-" {
		opt.ToStdout = true
		opt.OutFile = ""
	}

	// Get the provider
	provider := cmd.Flags().Lookup("provider").Value.String()
	log.Debugf("Checking validation of provider: %s", provider)

	// OpenShift specific flags
	deploymentConfig := cmd.Flags().Lookup("deployment-config").Changed
	buildRepo := cmd.Flags().Lookup("build-repo").Changed
	buildBranch := cmd.Flags().Lookup("build-branch").Changed

	// Kubernetes specific flags
	chart := cmd.Flags().Lookup("chart").Changed
	daemonSet := cmd.Flags().Lookup("daemon-set").Changed
	replicationController := cmd.Flags().Lookup("replication-controller").Changed
	deployment := cmd.Flags().Lookup("deployment").Changed

	// Get the controller
	controller := opt.Controller
	log.Debugf("Checking validation of controller: %s", controller)

	// Check validations against provider flags
	switch {
	case provider == ProviderOpenshift:
		if chart {
			return fmt.Errorf("--chart, -c is a Kubernetes only flag")
		}
		if daemonSet {
			return fmt.Errorf("--daemon-set is a Kubernetes only flag")
		}
		if replicationController {
			return fmt.Errorf("--replication-controller is a Kubernetes only flag")
		}
		if deployment {
			return fmt.Errorf("--deployment, -d is a Kubernetes only flag")
		}
		if controller == "daemonset" || controller == "replicationcontroller" || controller == "deployment" {
			return fmt.Errorf("--controller= daemonset, replicationcontroller or deployment is a Kubernetes only flag")
		}
	case provider == ProviderKubernetes:
		if deploymentConfig {
			return fmt.Errorf("--deployment-config is an OpenShift only flag")
		}
		if buildRepo {
			return fmt.Errorf("--build-repo is an Openshift only flag")
		}
		if buildBranch {
			return fmt.Errorf("--build-branch is an Openshift only flag")
		}
		if controller == "deploymentconfig" {
			return fmt.Errorf("--controller=deploymentConfig is an OpenShift only flag")
		}
	}

	// Standard checks regardless of provider
	if len(opt.OutFile) != 0 && opt.ToStdout {
		return fmt.Errorf("Error: --out and --stdout can't be set at the same time")
	}

	if opt.CreateChart && opt.ToStdout {
		return fmt.Errorf("Error: chart cannot be generated when --stdout is specified")
	}

	if opt.Replicas < 0 {
		return fmt.Errorf("Error: --replicas cannot be negative")
	}

	if len(args) != 0 {
		return fmt.Errorf("Unknown Argument(s): %s", strings.Join(args, ","))
	}

	if opt.GenerateJSON && opt.GenerateYaml {
		return fmt.Errorf("YAML and JSON format cannot be provided at the same time")
	}

	if _, ok := kubernetes.ValidVolumeSet[opt.Volumes]; !ok {
		validVolumesTypes := make([]string, 0)
		for validVolumeType := range kubernetes.ValidVolumeSet {
			validVolumesTypes = append(validVolumesTypes, fmt.Sprintf("'%s'", validVolumeType))
		}
		return fmt.Errorf("Unknown Volume type: %s %s %s", opt.Volumes, ", possible values are: ", strings.Join(validVolumesTypes, " "))
	}
	return nil
}

// ValidateComposeFile validates the compose file provided for conversion
func ValidateComposeFile(opt *kobject.ConvertOptions) error {
	if len(opt.InputFiles) == 0 {
		for _, name := range DefaultComposeFiles {
			_, err := os.Stat(name)
			if err != nil {
				log.Debugf("'%s' not found: %v", name, err)
			} else {
				opt.InputFiles = []string{name}
			}
		}

		return fmt.Errorf("No 'docker-compose' file found")
	}
	return nil
}

func validateControllers(opt *kobject.ConvertOptions) error {
	singleOutput := len(opt.OutFile) != 0 || opt.OutFile == "-" || opt.ToStdout
	if opt.Provider == ProviderKubernetes {
		// create deployment by default if no controller has been set
		if !opt.CreateD && !opt.CreateDS && !opt.CreateRC && opt.Controller == "" {
			opt.CreateD = true
		}
		if singleOutput {
			count := 0
			if opt.CreateD {
				count++
			}
			if opt.CreateDS {
				count++
			}
			if opt.CreateRC {
				count++
			}
			if count > 1 {
				return fmt.Errorf("Error: only one kind of Kubernetes resource can be generated when --out or --stdout is specified")
			}
		}
	} else if opt.Provider == ProviderOpenshift {
		// create deploymentconfig by default if no controller has been set
		if !opt.CreateDeploymentConfig {
			opt.CreateDeploymentConfig = true
		}
		if singleOutput {
			count := 0
			if opt.CreateDeploymentConfig {
				count++
			}
			// Add more controllers here once they are available in OpenShift
			// if opt.foo {count++}

			if count > 1 {
				return fmt.Errorf("Error: only one kind of OpenShift resource can be generated when --out or --stdout is specified")
			}
		}
	}
	return nil
}

// Convert transforms docker compose or dab file to k8s objects
func Convert(opt kobject.ConvertOptions) error {
	err := validateControllers(&opt)
	if err != nil {
		return err
	}
	// loader parses input from file into komposeObject.
	l, err := loader.GetLoader(inputFormat)
	if err != nil {
		return err
	}

	komposeObject := kobject.KomposeObject{
		ServiceConfigs: make(map[string]kobject.ServiceConfig),
	}
	komposeObject, err = l.LoadFile(opt.InputFiles)
	if err != nil {
		return err
	}

	// Get a transformer that maps komposeObject to provider's primitives
	t := getTransformer(opt)

	// Do the transformation
	objects, err := t.Transform(komposeObject, opt)

	if err != nil {
		return err
	}

	// Print output
	err = kubernetes.PrintList(objects, opt)
	if err != nil {
		return err
	}
	return nil
}

// Convenience method to return the appropriate Transformer based on
// what provider we are using.
func getTransformer(opt kobject.ConvertOptions) transformer.Transformer {
	var t transformer.Transformer
	if opt.Provider == DefaultProvider {
		// Create/Init new Kubernetes object with CLI opts
		t = &kubernetes.Kubernetes{Opt: opt}
	} else {
		// Create/Init new OpenShift object that is initialized with a newly
		// created Kubernetes object. Openshift inherits from Kubernetes
		t = &openshift.OpenShift{Kubernetes: kubernetes.Kubernetes{Opt: opt}}
	}
	return t
}
