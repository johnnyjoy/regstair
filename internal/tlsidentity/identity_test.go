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

func TestEnsureReissuesServerCertificateForConfiguredHostsWithoutReplacingCA(t *testing.T) {
	dir := t.TempDir()
	identity, err := Ensure(dir, []string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	caBefore, err := os.ReadFile(identity.CACertFile)
	if err != nil {
		t.Fatal(err)
	}
	keyBefore, err := os.ReadFile(identity.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	certBefore, err := os.ReadFile(identity.CertFile)
	if err != nil {
		t.Fatal(err)
	}

	updated, err := Ensure(dir, []string{"localhost", "regstair.example.test", "10.20.30.40"})
	if err != nil {
		t.Fatal(err)
	}
	caAfter, _ := os.ReadFile(updated.CACertFile)
	keyAfter, _ := os.ReadFile(updated.KeyFile)
	certAfter, _ := os.ReadFile(updated.CertFile)
	if string(caBefore) != string(caAfter) {
		t.Fatal("certificate authority changed during server certificate reissue")
	}
	if string(keyBefore) != string(keyAfter) {
		t.Fatal("server private key changed during certificate reissue")
	}
	if string(certBefore) == string(certAfter) {
		t.Fatal("server certificate was not reissued")
	}
	pair, err := tls.LoadX509KeyPair(updated.CertFile, updated.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"localhost", "regstair.example.test", "10.20.30.40"} {
		if err := leaf.VerifyHostname(host); err != nil {
			t.Fatalf("reissued certificate does not cover %q: %v", host, err)
		}
	}
}

func TestEnsureExactRemovesStaleSubjectAlternativeNames(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureExact(dir, []string{"localhost", "10.1.1.79"}); err != nil {
		t.Fatal(err)
	}
	identity, err := EnsureExact(dir, []string{"localhost", "10.1.1.82"})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(identity.CertFile, identity.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("10.1.1.82"); err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("10.1.1.79"); err == nil {
		t.Fatal("stale host remains in exact certificate")
	}
}

func TestEnsureRequiresAtLeastOneSubjectAlternativeName(t *testing.T) {
	if _, err := Ensure(t.TempDir(), []string{"", "   "}); err == nil {
		t.Fatal("Ensure accepted no subject alternative names")
	}
}

func TestUsableAddressStringsKeepsRoutableHostAddresses(t *testing.T) {
	addresses := []net.Addr{
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("10.1.1.79"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("10.1.1.79"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("2001:db8::25"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
	}
	got := usableAddressStrings(addresses)
	if len(got) != 2 || got[0] != "10.1.1.79" || got[1] != "2001:db8::25" {
		t.Fatalf("usable addresses = %v", got)
	}
}

func TestIgnoredLinuxInterfaceExcludesContainerBridges(t *testing.T) {
	for _, name := range []string{"docker0", "br-deadbeef", "veth1234", "virbr0"} {
		if !ignoredLinuxInterface(name) {
			t.Fatalf("interface %q was not ignored", name)
		}
	}
	for _, name := range []string{"eth0", "eno1", "wlan0", "bond0", "tailscale0"} {
		if ignoredLinuxInterface(name) {
			t.Fatalf("interface %q was unexpectedly ignored", name)
		}
	}
}
