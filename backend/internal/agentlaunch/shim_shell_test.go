package agentlaunch

import (
	"os/exec"
	"testing"
)

func TestAndroidShimUsesNativeShell(t *testing.T) {
	want, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh unavailable")
	}
	got, err := shimShell("android")
	if err != nil || got != want {
		t.Fatalf("shell=%q err=%v want=%q", got, err, want)
	}
	got, err = shimShell("linux")
	if err != nil || got != "/bin/sh" {
		t.Fatalf("desktop shell=%q err=%v", got, err)
	}
}
