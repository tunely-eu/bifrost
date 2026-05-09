package client

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bifrost/internal/config"
	"bifrost/internal/multiplex"
	"bifrost/internal/protocol"
)

func TestRunStopsOnContextCancelWhileWaitingForStreams(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, caFile := writeSelfSignedCert(t, dir)
	targetAddr, stopTarget := startDiscardTarget(t)
	defer stopTarget()

	serverAddr, stopServer := startIdleTunnelServer(t, certFile, keyFile)
	defer stopServer()

	cfg := config.DefaultClientConfig()
	cfg.Client.ServerURL = serverAddr
	cfg.Client.Headers = map[string]string{"X-Bifrost-Token": "dev-secret"}
	cfg.Client.Target = config.Target{Type: "tcp", Address: targetAddr}
	cfg.Client.TLS.CAFile = caFile
	cfg.Client.TLS.ServerName = "localhost"
	cfg.ApplyDefaults()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, cfg, Options{
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not stop after context cancellation")
	}
}

func startIdleTunnelServer(t *testing.T, certFile string, keyFile string) (string, func()) {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("load cert: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					return
				}
			}
			go func(raw net.Conn) {
				defer raw.Close()
				conn := tls.Server(raw, &tls.Config{
					Certificates: []tls.Certificate{cert},
					NextProtos:   []string{protocol.ALPN},
					MinVersion:   tls.VersionTLS12,
				})
				if err := conn.Handshake(); err != nil {
					return
				}
				var hello protocol.Hello
				if err := protocol.ReadJSONLine(conn, &hello, protocol.DefaultMaxLineBytes); err != nil {
					return
				}
				if err := protocol.WriteJSONLine(conn, protocol.Response{Accepted: true}, protocol.DefaultMaxLineBytes); err != nil {
					return
				}
				session, err := multiplex.Server(conn, 0)
				if err != nil {
					return
				}
				defer session.Close()
				<-session.CloseChan()
			}(raw)
		}
	}()
	return ln.Addr().String(), func() {
		cancel()
		_ = ln.Close()
	}
}

func startDiscardTarget(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
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
				_, _ = io.Copy(io.Discard, conn)
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
