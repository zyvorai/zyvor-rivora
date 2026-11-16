// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0
package healthcheck

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/zyvorai/rivora/internal/config"
)

func httpsTarget(t *testing.T, srv *httptest.Server, spec config.ProbeSpec) Target {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p, _ := strconv.Atoi(portStr)
	spec.Type = config.ProbeHTTPS
	return Target{BackendID: 1, Address: host, Port: uint16(p), Probe: spec}
}

// selfSignedTLS returns a tls.Config that presents a self-signed cert for name.
// HTTPS probes skip verification, so the name only matters for SNI selection.
func selfSignedTLS(t *testing.T, name string) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{name},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
}

func httpsStatusServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}))
	srv.TLS = selfSignedTLS(t, "health.example")
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPSProbeJudgesTheStatus(t *testing.T) {
	for code, want := range map[int]bool{200: true, 302: true, 500: false} {
		srv := httpsStatusServer(t, code)
		if got := probe(httpsTarget(t, srv, config.ProbeSpec{}), time.Second); got != want {
			t.Errorf("status %d: probe = %v, want %v", code, got, want)
		}
	}
}

// A self-signed (or name-mismatched) cert must not take a healthy backend out of
// rotation: probes dial by IP and health endpoints rarely present a matching cert.
func TestHTTPSProbeAcceptsSelfSignedCert(t *testing.T) {
	srv := httpsStatusServer(t, 200)
	if !probe(httpsTarget(t, srv, config.ProbeSpec{}), time.Second) {
		t.Error("HTTPS probe failed against a self-signed certificate")
	}
}

func TestHTTPSProbeUsesPathAndHostHeader(t *testing.T) {
	var gotPath, gotHost string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHost = r.URL.RequestURI(), r.Host
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = selfSignedTLS(t, "app.example")
	srv.StartTLS()
	t.Cleanup(srv.Close)

	if !probe(httpsTarget(t, srv, config.ProbeSpec{Path: "/ready?deep=1", Host: "app.example"}), time.Second) {
		t.Fatal("HTTPS probe failed")
	}
	if gotPath != "/ready?deep=1" || gotHost != "app.example" {
		t.Errorf("request = path %q host %q, want /ready?deep=1 and app.example", gotPath, gotHost)
	}
}

func TestHTTPSProbeCatchesWhatTCPMisses(t *testing.T) {
	srv := httpsStatusServer(t, http.StatusServiceUnavailable)
	tgt := httpsTarget(t, srv, config.ProbeSpec{})
	tcp := tgt
	tcp.Probe = config.ProbeSpec{} // legacy TCP default
	if !probe(tcp, time.Second) {
		t.Fatal("a TCP probe should pass a backend that accepts connections")
	}
	if probe(tgt, time.Second) {
		t.Error("an HTTPS probe passed a backend answering 503")
	}
}

func TestHTTPSProbeFailsAgainstCleartextHTTP(t *testing.T) {
	srv := statusServer(t, 200) // plain HTTP
	if probe(httpsTarget(t, srv, config.ProbeSpec{}), time.Second) {
		t.Error("an HTTPS probe passed a cleartext HTTP listener")
	}
}

func TestHTTPSProbeTimesOutOnASlowBackend(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = selfSignedTLS(t, "slow.example")
	srv.StartTLS()
	t.Cleanup(srv.Close)

	start := time.Now()
	if probe(httpsTarget(t, srv, config.ProbeSpec{}), 200*time.Millisecond) {
		t.Error("slow HTTPS backend passed a short-timeout probe")
	}
	if d := time.Since(start); d > 800*time.Millisecond {
		t.Errorf("probe took %v; it must give up at its timeout, not hang", d)
	}
}
