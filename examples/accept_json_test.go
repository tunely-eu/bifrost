package examples

import (
	"os/exec"
	"strings"
	"testing"
)

func TestAcceptJSONHook(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed")
	}
	cmd := exec.Command("../docker/accept-json.sh", "--clients", "./clients.json")
	cmd.Stdin = strings.NewReader(`{"headers":{"x-bifrost-token":"dev-secret"}}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}
	if !strings.Contains(string(out), `"allow":true`) {
		t.Fatalf("hook output = %s", out)
	}

	cmd = exec.Command("../docker/accept-json.sh", "--clients", "./clients.json")
	cmd.Stdin = strings.NewReader(`{"headers":{"x-bifrost-token":"wrong"}}`)
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}
	if !strings.Contains(string(out), `"allow":false`) {
		t.Fatalf("hook output = %s", out)
	}
}
