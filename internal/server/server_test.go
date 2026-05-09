package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bifrost/internal/client"
	"bifrost/internal/config"
	"bifrost/internal/listener"
	"bifrost/internal/protocol"
	"bifrost/internal/server"
)

func TestTunnelForwardsBytesOverTLSUnixSocket(t *testing.T) {
	targetAddr, stopTarget := startEchoTarget(t)
	defer stopTarget()

	dir := t.TempDir()
	certFile, keyFile, caFile := writeSelfSignedCert(t, dir)
	socketPath := filepath.Join(dir, "dev.sock")
	hookPath := writeAcceptHook(t, fmt.Sprintf(`{"allow":true,"reason":"accepted","endpoint_key":"dev","listener":{"type":"unix","path":%q,"mode":"0600"},"connection_policy":{"mode":"replace_existing"},"limits":{"max_streams":50,"max_bandwidth_bps":100000000,"stream_idle_timeout_seconds":30}}`, socketPath))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverReady := make(chan net.Addr, 1)
	listenerReady := make(chan listener.Spec, 1)
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(ctx, serverConfig(certFile, keyFile, hookPath, dir), server.Options{
			Logger: discardLogger(),
			Ready: func(addr net.Addr) {
				serverReady <- addr
			},
			ListenerReady: func(_ string, spec listener.Spec, _ net.Addr) {
				listenerReady <- spec
			},
		})
	}()

	serverAddr := waitAddr(t, serverReady, "server")
	clientErr := make(chan error, 1)
	go func() {
		clientErr <- client.Run(ctx, clientConfig(serverAddr.String(), targetAddr, caFile), client.Options{Logger: discardLogger()})
	}()

	spec := waitSpec(t, listenerReady)
	if spec.Path != socketPath {
		t.Fatalf("listener path = %q", spec.Path)
	}
	assertRoundTrip(t, "unix", socketPath, []byte("hello over bifrost"))

	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			errs <- roundTrip("unix", socketPath, []byte(fmt.Sprintf("parallel-%d", i)))
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	cancel()
	expectCleanShutdown(t, serverErr, "server")
	expectCleanShutdown(t, clientErr, "client")
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("unix socket was not removed: %v", err)
	}
}

func TestRejectIfExistsRejectsSecondSession(t *testing.T) {
	targetAddr, stopTarget := startEchoTarget(t)
	defer stopTarget()

	dir := t.TempDir()
	certFile, keyFile, caFile := writeSelfSignedCert(t, dir)
	hookPath := writeAcceptHook(t, `{"allow":true,"endpoint_key":"same","listener":{"type":"tcp","address":"127.0.0.1:0"},"connection_policy":{"mode":"reject_if_exists"},"limits":{"max_streams":10,"max_bandwidth_bps":100000000,"stream_idle_timeout_seconds":30}}`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverReady := make(chan net.Addr, 1)
	listenerReady := make(chan listener.Spec, 1)
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(ctx, serverConfig(certFile, keyFile, hookPath, dir), server.Options{
			Logger: discardLogger(),
			Ready: func(addr net.Addr) {
				serverReady <- addr
			},
			ListenerReady: func(_ string, spec listener.Spec, _ net.Addr) {
				listenerReady <- spec
			},
		})
	}()
	serverAddr := waitAddr(t, serverReady, "server")

	firstErr := make(chan error, 1)
	go func() {
		firstErr <- client.Run(ctx, clientConfig(serverAddr.String(), targetAddr, caFile), client.Options{Logger: discardLogger()})
	}()
	_ = waitSpec(t, listenerReady)

	rejectCtx, rejectCancel := context.WithCancel(ctx)
	defer rejectCancel()
	secondErr := make(chan error, 1)
	go func() {
		secondErr <- client.Run(rejectCtx, clientConfig(serverAddr.String(), targetAddr, caFile), client.Options{Logger: discardLogger()})
	}()

	select {
	case err := <-secondErr:
		if err == nil {
			t.Fatal("second client exited without error")
		}
	case <-time.After(300 * time.Millisecond):
		// The client reconnect loop keeps retrying rejected sessions; cancellation should stop it cleanly.
		rejectCancel()
		expectCleanShutdown(t, secondErr, "second client")
	}

	cancel()
	expectCleanShutdown(t, serverErr, "server")
	expectCleanShutdown(t, firstErr, "first client")
}

func TestServerRejectsWrongALPN(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, caFile := writeSelfSignedCert(t, dir)
	hookPath := writeAcceptHook(t, `{"allow":true,"endpoint_key":"dev","listener":{"type":"tcp","address":"127.0.0.1:0"}}`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverReady := make(chan net.Addr, 1)
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(ctx, serverConfig(certFile, keyFile, hookPath, dir), server.Options{
			Logger: discardLogger(),
			Ready: func(addr net.Addr) {
				serverReady <- addr
			},
		})
	}()
	serverAddr := waitAddr(t, serverReady, "server")

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("read ca: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("append ca")
	}
	conn, err := tls.Dial("tcp", serverAddr.String(), &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: "localhost",
		NextProtos: []string{"wrong/1"},
	})
	if err != nil {
		cancel()
		expectCleanShutdown(t, serverErr, "server")
		return
	}
	defer conn.Close()
	if got := conn.ConnectionState().NegotiatedProtocol; got != "" {
		t.Fatalf("negotiated alpn = %q", got)
	}
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := conn.Read(b[:]); err == nil {
		t.Fatal("expected server to close wrong ALPN connection")
	}

	cancel()
	expectCleanShutdown(t, serverErr, "server")
}

func TestServerKeepAliveTimeoutCleansUpSessionListener(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, caFile := writeSelfSignedCert(t, dir)
	socketPath := filepath.Join(dir, "stale.sock")
	hookPath := writeAcceptHook(t, fmt.Sprintf(`{"allow":true,"endpoint_key":"stale","listener":{"type":"unix","path":%q},"connection_policy":{"mode":"replace_existing"},"limits":{"max_streams":10,"max_bandwidth_bps":100000000,"stream_idle_timeout_seconds":30}}`, socketPath))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverReady := make(chan net.Addr, 1)
	listenerReady := make(chan listener.Spec, 1)
	serverErr := make(chan error, 1)
	cfg := serverConfig(certFile, keyFile, hookPath, dir)
	cfg.Runtime.TunnelKeepAliveInterval = config.NewDuration(20 * time.Millisecond)
	cfg.Runtime.TunnelKeepAliveTimeout = config.NewDuration(20 * time.Millisecond)
	go func() {
		serverErr <- server.Run(ctx, cfg, server.Options{
			Logger: discardLogger(),
			Ready: func(addr net.Addr) {
				serverReady <- addr
			},
			ListenerReady: func(_ string, spec listener.Spec, _ net.Addr) {
				listenerReady <- spec
			},
		})
	}()
	serverAddr := waitAddr(t, serverReady, "server")

	conn := openRawAcceptedTunnel(t, serverAddr.String(), caFile)
	defer conn.Close()
	_ = waitSpec(t, listenerReady)
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("expected socket to exist: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		_, err := os.Stat(socketPath)
		return os.IsNotExist(err)
	}, "unix socket cleanup after keepalive timeout")

	cancel()
	expectCleanShutdown(t, serverErr, "server")
}

func serverConfig(certFile, keyFile, hookPath, prefix string) config.ServerConfig {
	cfg := config.DefaultServerConfig()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.TLS.CertFile = certFile
	cfg.Server.TLS.KeyFile = keyFile
	cfg.AcceptHook.Command = hookPath
	cfg.Runtime.HookTimeout = config.NewDuration(time.Second)
	cfg.ListenerPolicy.AllowedUnixPrefixes = []string{prefix}
	cfg.ListenerPolicy.CreateParentDirs = true
	cfg.Guardrails.MaxSessions = 10
	cfg.Guardrails.MaxBandwidthBPSPerSession = 100_000_000
	cfg.ApplyDefaults()
	return cfg
}

func openRawAcceptedTunnel(t *testing.T, serverAddr, caFile string) *tls.Conn {
	t.Helper()
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("read ca: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("append ca")
	}
	conn, err := tls.Dial("tcp", serverAddr, &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: "localhost",
		NextProtos: []string{"bifrost/1"},
	})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	if err := protocol.WriteJSONLine(conn, map[string]any{
		"protocol_version": "1",
		"headers": map[string]string{
			"x-bifrost-token": "dev-secret",
		},
	}, protocol.DefaultMaxLineBytes); err != nil {
		_ = conn.Close()
		t.Fatalf("write hello: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var response struct {
		Accepted bool   `json:"accepted"`
		Reason   string `json:"reason"`
	}
	if err := protocol.ReadJSONLine(conn, &response, protocol.DefaultMaxLineBytes); err != nil {
		_ = conn.Close()
		t.Fatalf("read response: %v", err)
	}
	if !response.Accepted {
		_ = conn.Close()
		t.Fatalf("server rejected raw tunnel: %s", response.Reason)
	}
	_ = conn.SetReadDeadline(time.Time{})
	return conn
}

func clientConfig(serverAddr, targetAddr, caFile string) config.ClientConfig {
	cfg := config.DefaultClientConfig()
	cfg.Client.ServerURL = serverAddr
	cfg.Client.Headers = map[string]string{"X-Bifrost-Token": "dev-secret"}
	cfg.Client.Target = config.Target{Type: "tcp", Address: targetAddr}
	cfg.Client.TLS.CAFile = caFile
	cfg.Client.TLS.ServerName = "localhost"
	cfg.ApplyDefaults()
	return cfg
}

func writeAcceptHook(t *testing.T, output string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "accept.sh")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '" + stringsReplaceForSingleQuote(output) + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	return path
}

func stringsReplaceForSingleQuote(raw string) string {
	return strings.ReplaceAll(raw, "'", "'\\''")
}

func writeSelfSignedCert(t *testing.T, dir string) (string, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")
	caFile := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.WriteFile(caFile, certPEM, 0o644); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	return certFile, keyFile, caFile
}

func startEchoTarget(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					return
				}
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}(conn)
		}
	}()
	return ln.Addr().String(), func() {
		cancel()
		_ = ln.Close()
	}
}

func assertRoundTrip(t *testing.T, network string, addr string, payload []byte) {
	t.Helper()
	if err := roundTrip(network, addr, payload); err != nil {
		t.Fatal(err)
	}
}

func roundTrip(network string, addr string, payload []byte) error {
	conn, err := net.DialTimeout(network, addr, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		return err
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("round trip = %q, want %q", got, payload)
	}
	return nil
}

func waitAddr(t *testing.T, ch <-chan net.Addr, name string) net.Addr {
	t.Helper()
	select {
	case addr := <-ch:
		return addr
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}

func waitSpec(t *testing.T, ch <-chan listener.Spec) listener.Spec {
	t.Helper()
	select {
	case spec := <-ch:
		return spec
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for listener")
		return listener.Spec{}
	}
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool, name string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", name)
}

func expectCleanShutdown(t *testing.T, ch <-chan error, name string) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("%s returned error: %v", name, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not shut down", name)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
