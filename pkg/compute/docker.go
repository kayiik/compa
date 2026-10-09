package compute

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Detect reports what this computer offers Compitas: a container runtime and a
// browser a Compita outside a container can drive. The browser image has one;
// so does any machine with playwright-mcp installed.
func Detect() Capabilities {
	caps := DetectDocker()
	_, err := exec.LookPath("playwright-mcp")
	caps.Browser = err == nil
	return caps
}

// DetectDocker asks the local docker CLI for its server version. A missing
// binary, a stopped daemon and a permission error all mean "no Docker": a
// Compita cannot use a container runtime it cannot talk to.
func DetectDocker() Capabilities {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		return Capabilities{}
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return Capabilities{}
	}
	return Capabilities{Docker: true, DockerVersion: version}
}
