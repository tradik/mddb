package main

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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The follower dials its leader over TLS, with a client certificate when the
// leader requires one (GO-050); it used to dial in plaintext whatever was
// configured.

// testPKI is a CA with a server and a client certificate, as PEM files.
type testPKI struct {
	dir                  string
	ca                   *x509.Certificate
	caKey                *ecdsa.PrivateKey
	caFile               string
	serverCert, serverKy string
	clientCert, clientKy string
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	p := &testPKI{dir: t.TempDir()}
	p.caKey = mustKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &p.caKey.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	p.ca, _ = x509.ParseCertificate(der)
	p.caFile = p.writePEM(t, "ca.pem", "CERTIFICATE", der)
	p.serverCert, p.serverKy = p.issue(t, "server", x509.ExtKeyUsageServerAuth)
	p.clientCert, p.clientKy = p.issue(t, "client", x509.ExtKeyUsageClientAuth)
	return p
}

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func (p *testPKI) issue(t *testing.T, name string, usage x509.ExtKeyUsage) (certFile, keyFile string) {
	t.Helper()
	key := mustKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{usage}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames: []string{"leader.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.ca, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return p.writePEM(t, name+".pem", "CERTIFICATE", der), p.writePEM(t, name+".key", "EC PRIVATE KEY", kder)
}

func (p *testPKI) writePEM(t *testing.T, name, kind string, der []byte) string {
	t.Helper()
	path := filepath.Join(p.dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// serverTLS is the leader's listener: its certificate, client certificates required.
func (p *testPKI) serverTLS(t *testing.T) *tls.Config {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(p.serverCert, p.serverKy)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(p.ca)
	return &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: pool,
		ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
}

func TestE2EFollowerOverMutualTLS(t *testing.T) {
	p := newTestPKI(t)
	t.Setenv("MDDB_REPLICATION_TLS_CA", p.caFile)
	t.Setenv("MDDB_REPLICATION_TLS_CERT", p.clientCert)
	t.Setenv("MDDB_REPLICATION_TLS_KEY", p.clientKy)
	t.Setenv("MDDB_REPLICATION_TLS_SERVER_NAME", "leader.test")
	clientTLS, err := replicationTLSFromEnv()
	if err != nil || clientTLS == nil {
		t.Fatalf("replication TLS: %v, %v", clientTLS, err)
	}

	l := startE2ELeader(t, e2eLeaderOpts{dir: t.TempDir(), tls: p.serverTLS(t)})
	defer l.stop(t)
	l.write(t, "blog", 0, 20)
	f := startE2EFollower(t, e2eFollowerOpts{dir: t.TempDir(), leader: l.addr, tls: clientTLS})
	defer f.stop()
	waitE2E(t, "the follower to replicate over TLS", func() bool { return caughtUp(l, f) })
	assertSameContent(t, l, f)

	// The same leader refuses a follower that dials in plaintext.
	plain := startE2EFollower(t, e2eFollowerOpts{dir: t.TempDir(), leader: l.addr})
	defer plain.stop()
	waitE2E(t, "the plaintext follower to fail", func() bool { return plain.rc.lastError.Load() != nil })
	if plain.rc.applier.LastAppliedLSN() != 0 {
		t.Error("a plaintext follower received entries from a TLS leader")
	}
}

func TestReplicationTLSFromEnv(t *testing.T) {
	p := newTestPKI(t)
	cfg, err := replicationTLSFromEnv()
	if cfg != nil || err != nil {
		t.Fatalf("nothing set: %v, %v; want plaintext", cfg, err)
	}
	t.Setenv("MDDB_REPLICATION_TLS", "true")
	if cfg, err = replicationTLSFromEnv(); err != nil || cfg == nil || cfg.RootCAs != nil {
		t.Fatalf("TLS on with the system pool: %v, %v", cfg, err)
	}

	cases := []struct{ name, ca, cert, key, want string }{
		{"missing CA", filepath.Join(p.dir, "none.pem"), "", "", "MDDB_REPLICATION_TLS_CA"},
		{"CA without PEM", p.clientKy + "x", "", "", "MDDB_REPLICATION_TLS_CA"},
		{"certificate without key", "", p.clientCert, "", "go together"},
		{"key that is not the certificate's", "", p.clientCert, p.serverKy, "client certificate"},
	}
	if err := os.WriteFile(p.clientKy+"x", []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Setenv("MDDB_REPLICATION_TLS_CA", c.ca)
		t.Setenv("MDDB_REPLICATION_TLS_CERT", c.cert)
		t.Setenv("MDDB_REPLICATION_TLS_KEY", c.key)
		if _, err := replicationTLSFromEnv(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", c.name, err, c.want)
		}
	}
}

func TestBinlogConfigFromEnv(t *testing.T) {
	cfg, err := binlogConfigFromEnv()
	if err != nil || cfg.Sync != "interval" || cfg.MaxAge != 0 {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}
	t.Setenv("MDDB_BINLOG_SYNC", "COMMIT")
	t.Setenv("MDDB_BINLOG_MAX_AGE", "2h")
	t.Setenv("MDDB_BINLOG_MAX_SIZE", "1024")
	if cfg, err = binlogConfigFromEnv(); err != nil || cfg.Sync != "commit" || cfg.MaxAge != 2*time.Hour || cfg.MaxSize != 1024 {
		t.Fatalf("set: %+v, %v", cfg, err)
	}
	for k, v := range map[string]string{"MDDB_BINLOG_SYNC": "always", "MDDB_BINLOG_MAX_AGE": "-1h"} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, v)
			if _, err := binlogConfigFromEnv(); err == nil || !strings.Contains(err.Error(), k) {
				t.Errorf("%s=%s: %v", k, v, err)
			}
		})
	}
}
