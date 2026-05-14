package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExampleConfigs(t *testing.T) {
	dir := t.TempDir()
	serverPath := filepath.Join(dir, "server.yaml")
	clientPath := filepath.Join(dir, "client.yaml")
	if err := os.WriteFile(serverPath, []byte(ExampleServerYAML), 0o644); err != nil {
		t.Fatalf("write server config: %v", err)
	}
	if err := os.WriteFile(clientPath, []byte(ExampleClientYAML), 0o644); err != nil {
		t.Fatalf("write client config: %v", err)
	}
	serverCfg, err := LoadServerFile(serverPath)
	if err != nil {
		t.Fatalf("LoadServerFile: %v", err)
	}
	if serverCfg.Guardrails.MaxHeaders != 32 {
		t.Fatalf("MaxHeaders = %d", serverCfg.Guardrails.MaxHeaders)
	}
	if serverCfg.Runtime.TunnelKeepAliveInterval.Duration == 0 {
		t.Fatal("expected keepalive interval")
	}
	clientCfg, err := LoadClientFile(clientPath)
	if err != nil {
		t.Fatalf("LoadClientFile: %v", err)
	}
	if clientCfg.Client.Headers["X-Bifrost-Token"] != "dev-secret" {
		t.Fatalf("token header = %q", clientCfg.Client.Headers["X-Bifrost-Token"])
	}
}

func TestLoadJSONConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.json")
	raw := `{"client":{"server_url":"localhost:8443","target":{"type":"tcp","address":"127.0.0.1:8080"},"tls":{"insecure_skip_verify":true}}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadClientFile(path)
	if err != nil {
		t.Fatalf("LoadClientFile: %v", err)
	}
	if cfg.Logging.Level == "" {
		t.Fatal("expected logging defaults")
	}
}

func TestServerConfigValidation(t *testing.T) {
	cfg := DefaultServerConfig()
	cfg.Server.TLS.CertFile = ""
	cfg.Server.TLS.KeyFile = "key.pem"
	cfg.Clients = []Client{{Token: "secret", EndpointKey: "home"}}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing cert_file error")
	}
}

func TestServerConfigValidationAllowsExternalTLS(t *testing.T) {
	cfg := DefaultServerConfig()
	cfg.Server.TLS.CertFile = ""
	cfg.Server.TLS.KeyFile = ""
	cfg.Clients = []Client{{Token: "secret", EndpointKey: "home"}}
	cfg.ApplyDefaults()
	if err := cfg.ValidateWithOptions(ValidationOptions{ExternalTLSConfigured: true}); err != nil {
		t.Fatalf("ValidateWithOptions: %v", err)
	}
}
