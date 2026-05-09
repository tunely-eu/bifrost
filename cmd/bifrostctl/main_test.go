package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConfigExampleAndValidate(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "config", "example", "--client")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("config example: %v", err)
	}
	path := filepath.Join(t.TempDir(), "client.yaml")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write client config: %v", err)
	}
	cmd = exec.Command("go", "run", ".", "config", "validate", "--client", "--file", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("config validate: %v\n%s", err, out)
	}
}
