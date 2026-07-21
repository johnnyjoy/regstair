package tlsidentity

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureCreatesPersistentCAAndServerIdentity(t *testing.T) {
	dir := t.TempDir()
	identity, err := Ensure(dir, []string{"regstair.example.test", "10.20.30.40", "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{identity.CACertFile, identity.CAKeyFile, identity.CertFile, identity.KeyFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("generated file %s: %v", path, err)
		}
	}
	for _, path := range []string{identity.CAKeyFile, identity.KeyFile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("private key mode = %o, want 600", got)
		}
	}
	pair, err := tls.LoadX509KeyPair(identity.CertFile, identity.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("regstair.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("10.20.30.40"); err != nil {
		t.Fatal(err)
	}
	if !leaf.IPAddresses[0].Equal(net.ParseIP("10.20.30.40")) {
		t.Fatalf("IP SANs = %v", leaf.IPAddresses)
	}
	first, err := os.ReadFile(identity.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Ensure(dir, []string{"regstair.example.test", "10.20.30.40", "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(again.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("existing server certificate was unexpectedly regenerated")
	}
}

func TestEnsureRejectsCorruptExistingIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, serverCertName), []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(dir, []string{"localhost"}); err == nil {
		t.Fatal("Ensure accepted a partial corrupt identity")
	}
}

func TestEnsureRejectsExistingIdentityThatDoesNotCoverConfiguredHost(t *testing.T) {
	dir := t.TempDir()
	if _, err := Ensure(dir, []string{"localhost"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(dir, []string{"localhost", "regstair.example.test"}); err == nil {
		t.Fatal("Ensure accepted an existing certificate without the configured DNS SAN")
	}
}

func TestEnsureRequiresAtLeastOneSubjectAlternativeName(t *testing.T) {
	if _, err := Ensure(t.TempDir(), []string{"", "   "}); err == nil {
		t.Fatal("Ensure accepted no subject alternative names")
	}
}
