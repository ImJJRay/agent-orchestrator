package agentlaunch

import (
	"fmt"
	"os/exec"
)

func shimShell(goos string) (string, error) {
	if goos != "android" {
		return "/bin/sh", nil
	}
	// Android has no /bin/sh. Resolve the native Termux shell; do not rely
	// on termux-exec rewriting an invalid interpreter for every child process.
	shell, err := exec.LookPath("sh")
	if err != nil {
		return "", fmt.Errorf("android AO hook shim requires sh on PATH: %w", err)
	}
	return shell, nil
}
