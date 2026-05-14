package bifrost

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLibraryServerClientOpenStream(t *testing.T) {
	targetAddr, stopTarget := startEchoTarget(t)
	defer stopTarget()

	dir := t.TempDir()
	certFile, keyFile, caFile := writeSelfSignedCert(t, dir)

	serverReady := make(chan net.Addr, 1)
	server, err := NewServer(ServerConfig{
		Listen:      "127.0.0.1:0",
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
		Clients: []StaticClient{
			{
				Token:       "secret",
				EndpointKey: "home",
				ConnectionPolicy: ConnectionPolicy{
					Mode: PolicyReplaceExisting,
				},
				Limits: PlanLimits{
					MaxStreams:               10,
					MaxBandwidthBPS:          100_000_000,
					StreamIdleTimeoutSeconds: 30,
				},
			},
		},
	}, ServerOptions{Ready: func(addr net.Addr) { serverReady <- addr }})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(ctx)
	}()
	serverAddr := waitAddr(t, serverReady, "server")

	clientErr := make(chan error, 1)
	go func() {
		clientErr <- RunClient(ctx, ClientConfig{
			ServerURL:     serverAddr.String(),
			Headers:       map[string]string{TokenHeader: "secret"},
			TLSCAFile:     caFile,
			TLSServerName: "localhost",
		}, ClientOptions{
			StreamHandler: func(ctx context.Context, stream net.Conn) {
				target, err := (&net.Dialer{}).DialContext(ctx, "tcp", targetAddr)
				if err != nil {
					_ = stream.Close()
					return
				}
				Copy(ctx, stream, target, CopyOptions{})
			},
		})
	}()

	stream := waitStream(t, server, "home")
	if _, err := stream.Write([]byte("hello")); err != nil {
		t.Fatalf("write stream: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if string(buf) != "hello" {
		t.Fatalf("echo = %q", buf)
	}
	_ = stream.Close()

	cancel()
	expectCleanShutdown(t, serverErr, "server")
	expectCleanShutdown(t, clientErr, "client")
}

func TestLibraryServerClientOpenStreamWithExternalTLSConfig(t *testing.T) {
	targetAddr, stopTarget := startEchoTarget(t)
	defer stopTarget()

	dir := t.TempDir()
	certFile, keyFile, caFile := writeSelfSignedCert(t, dir)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("load cert: %v", err)
	}

	serverReady := make(chan net.Addr, 1)
	server, err := NewServer(ServerConfig{
		Listen:    "127.0.0.1:0",
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
		Clients: []StaticClient{
			{
				Token:       "secret",
				EndpointKey: "home",
				ConnectionPolicy: ConnectionPolicy{
					Mode: PolicyReplaceExisting,
				},
				Limits: PlanLimits{
					MaxStreams:               10,
					MaxBandwidthBPS:          100_000_000,
					StreamIdleTimeoutSeconds: 30,
				},
			},
		},
	}, ServerOptions{Ready: func(addr net.Addr) { serverReady <- addr }})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(ctx)
	}()
	serverAddr := waitAddr(t, serverReady, "server")

	clientErr := make(chan error, 1)
	go func() {
		clientErr <- RunClient(ctx, ClientConfig{
			ServerURL:     serverAddr.String(),
			Headers:       map[string]string{TokenHeader: "secret"},
			TLSCAFile:     caFile,
			TLSServerName: "localhost",
		}, ClientOptions{
			StreamHandler: func(ctx context.Context, stream net.Conn) {
				target, err := (&net.Dialer{}).DialContext(ctx, "tcp", targetAddr)
				if err != nil {
					_ = stream.Close()
					return
				}
				Copy(ctx, stream, target, CopyOptions{})
			},
		})
	}()

	stream := waitStream(t, server, "home")
	if _, err := stream.Write([]byte("hello")); err != nil {
		t.Fatalf("write stream: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if string(buf) != "hello" {
		t.Fatalf("echo = %q", buf)
	}
	_ = stream.Close()

	cancel()
	expectCleanShutdown(t, serverErr, "server")
	expectCleanShutdown(t, clientErr, "client")
}

func TestLibraryServerUsesExternalListener(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, _ := writeSelfSignedCert(t, dir)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("load cert: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen external: %v", err)
	}

	serverReady := make(chan net.Addr, 1)
	server, err := NewServer(ServerConfig{
		Listen:    ln.Addr().String(),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
		Clients: []StaticClient{
			{
				Token:       "secret",
				EndpointKey: "home",
				ConnectionPolicy: ConnectionPolicy{
					Mode: PolicyReplaceExisting,
				},
				Limits: PlanLimits{
					MaxStreams:               10,
					MaxBandwidthBPS:          100_000_000,
					StreamIdleTimeoutSeconds: 30,
				},
			},
		},
	}, ServerOptions{
		Listener: ln,
		Ready:    func(addr net.Addr) { serverReady <- addr },
	})
	if err != nil {
		_ = ln.Close()
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(ctx)
	}()
	serverAddr := waitAddr(t, serverReady, "server")
	if serverAddr.String() != ln.Addr().String() {
		t.Fatalf("ready addr = %q, want %q", serverAddr.String(), ln.Addr().String())
	}

	cancel()
	expectCleanShutdown(t, serverErr, "server")
}

func waitStream(t *testing.T, server *Server, endpoint string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		stream, err := server.OpenStream(context.Background(), endpoint)
		if err == nil {
			return stream
		}
		if time.Now().After(deadline) {
			t.Fatalf("wait stream: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitAddr(t *testing.T, ch <-chan net.Addr, name string) net.Addr {
	t.Helper()
	select {
	case addr := <-ch:
		return addr
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}

func expectCleanShutdown(t *testing.T, ch <-chan error, name string) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("%s returned error: %v", name, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not stop", name)
	}
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
	for path, data := range map[string][]byte{certFile: certPEM, keyFile: keyPEM, caFile: certPEM} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return certFile, keyFile, caFile
}

func TestLibraryRejectsDuplicateStaticTokens(t *testing.T) {
	_, err := NewStaticAcceptProvider([]StaticClient{
		{Token: "secret", EndpointKey: "home"},
		{Token: "secret", EndpointKey: "files"},
	})
	if err == nil {
		t.Fatal("expected duplicate token error")
	}
	if got := fmt.Sprint(err); got == "" {
		t.Fatal("expected error text")
	}
}
