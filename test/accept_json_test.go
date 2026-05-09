package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcceptJSONHook(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed")
	}
	dir := t.TempDir()
	clientsFile := filepath.Join(dir, "clients.json")
	if err := os.WriteFile(clientsFile, []byte(`{
  "clients": [
    {
      "token": "dev-secret",
      "endpoint_key": "homelab-dashboard",
      "listener": {
        "type": "unix",
        "path": "/sockets/homelab-dashboard.sock",
        "mode": "0600"
      },
      "connection_policy": {
        "mode": "replace_existing",
        "max_parallel": 1
      },
      "limits": {
        "max_streams": 100,
        "max_bandwidth_bps": 25000000,
        "stream_idle_timeout_seconds": 300
      },
      "labels": {
        "site": "homelab",
        "service": "dashboard"
      }
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write clients file: %v", err)
	}

	assertHookDecision(t, clientsFile, "dev-secret", `"allow":true`)
	assertHookDecision(t, clientsFile, "wrong", `"allow":false`)
}

func assertHookDecision(t *testing.T, clientsFile, token, want string) {
	t.Helper()
	cmd := exec.Command("../docker/accept-json.sh", "--clients", clientsFile)
	cmd.Stdin = strings.NewReader(`{"headers":{"x-bifrost-token":"` + token + `"}}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("hook output = %s", out)
	}
}
