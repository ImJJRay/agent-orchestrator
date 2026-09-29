package mobilebridge

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTailnetOnlyBlocksAllCloudflaredDiscovery(t *testing.T) {
	t.Setenv("AO_MOBILE_TAILNET_ONLY", "1")
	t.Setenv("AO_CLOUDFLARED_PATH", "/explicit/cloudflared")
	got := ResolveCloudflared(LocalCloudflaredLookup(t.TempDir()))
	if got.Path != "" || !got.NeedsInstall || got.Source != CloudflaredAbsent {
		t.Fatalf("unexpected public connector: %+v", got)
	}
}

func TestTailscaleExplicitBinaryAndSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable fixture")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "tailscale fixture")
	// The arguments are printed verbatim: spaces must remain in one socket flag.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_TAILSCALE_BINARY", binary)
	t.Setenv("AO_TAILSCALE_SOCKET", filepath.Join(dir, "private socket"))
	out, err := execTailscale(context.Background(), "serve", "status", "--json")
	want := "--socket=" + filepath.Join(dir, "private socket") + "\nserve\nstatus\n--json\n"
	if err != nil || string(out) != want {
		t.Fatalf("out=%q err=%v want=%q", out, err, want)
	}
	t.Setenv("AO_TAILSCALE_SOCKET", "")
	out, err = execTailscale(context.Background(), "status", "--json")
	if err != nil || strings.Contains(string(out), "socket") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
