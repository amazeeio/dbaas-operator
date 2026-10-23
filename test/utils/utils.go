package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProjectRoot returns the repository root, assuming tests run from test/e2e.
func ProjectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Clean(strings.Replace(wd, "/test/e2e", "", 1)), nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// Kubectl returns the kubectl binary to use.
func Kubectl() string { return envOr("KUBECTL_PATH", "kubectl") }

// Kind returns the kind binary to use.
func Kind() string { return envOr("KIND_PATH", "kind") }

// KindCluster returns the name of the kind cluster under test.
func KindCluster() string { return envOr("KIND_CLUSTER", "dbaas-operator-test") }

// Run executes a command from the project root and returns combined output.
func Run(name string, args ...string) (string, error) {
	root, err := ProjectRoot()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s %s failed: %w\n%s", name, strings.Join(args, " "), err, output)
	}
	return string(output), nil
}

// RunKubectl runs kubectl with the given arguments.
func RunKubectl(args ...string) (string, error) {
	return Run(Kubectl(), args...)
}

// Compose runs a docker compose command.
func Compose(args ...string) (string, error) {
	return Run("docker", append([]string{"compose"}, args...)...)
}

// LoadImageToKindClusterWithName loads a local docker image into the kind cluster.
func LoadImageToKindClusterWithName(name string) error {
	_, err := Run(Kind(), "load", "docker-image", name, "--name", KindCluster())
	return err
}

// GetNonEmptyLines splits output into its non-empty lines.
func GetNonEmptyLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
