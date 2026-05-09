package listener

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestUnixSocketListenAndCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.sock")
	ln, err := Listen(Spec{Type: "unix", Path: path, Mode: "0600"}, Options{
		AllowedUnixPrefixes: []string{dir},
		CreateParentDirs:    true,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v", info.Mode().Perm())
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket still exists: %v", err)
	}
}

func TestValidateRejectsPublicTCPByDefault(t *testing.T) {
	if err := Validate(Spec{Type: "tcp", Address: ":10001"}, Options{}); err == nil {
		t.Fatal("expected public tcp rejection")
	}
	if err := Validate(Spec{Type: "tcp", Address: "127.0.0.1:10001"}, Options{}); err != nil {
		t.Fatalf("localhost tcp rejected: %v", err)
	}
}

func TestValidateRejectsUnixPathOutsidePrefix(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(t.TempDir(), "dev.sock")
	if err := Validate(Spec{Type: "unix", Path: other}, Options{AllowedUnixPrefixes: []string{dir}}); err == nil {
		t.Fatal("expected path prefix rejection")
	}
}

func TestTCPListenLocalhost(t *testing.T) {
	ln, err := Listen(Spec{Type: "tcp", Address: "127.0.0.1:0"}, Options{})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	if _, ok := ln.Addr().(*net.TCPAddr); !ok {
		t.Fatalf("addr type = %T", ln.Addr())
	}
}
