package tlsidentity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	caCertName     = "regstair-ca.crt"
	caKeyName      = "regstair-ca.key"
	serverCertName = "regstair.crt"
	serverKeyName  = "regstair.key"
)

type Identity struct {
	CACertFile string
	CAKeyFile  string
	CertFile   string
	KeyFile    string
}

func Ensure(dir string, hosts []string) (Identity, error) {
	return ensure(dir, hosts, false)
}

// EnsureExact creates or reissues an identity whose SANs exactly match hosts.
func EnsureExact(dir string, hosts []string) (Identity, error) {
	return ensure(dir, hosts, true)
}

func ensure(dir string, hosts []string, exact bool) (Identity, error) {
	identity := Identity{
		CACertFile: filepath.Join(dir, caCertName),
		CAKeyFile:  filepath.Join(dir, caKeyName),
		CertFile:   filepath.Join(dir, serverCertName),
		KeyFile:    filepath.Join(dir, serverKeyName),
	}
	dnsNames, ipAddresses := normalizeHosts(hosts)
	if len(dnsNames) == 0 && len(ipAddresses) == 0 {
		return Identity{}, fmt.Errorf("at least one TLS DNS name or IP address is required")
	}
	existing := 0
	for _, path := range []string{identity.CACertFile, identity.CAKeyFile, identity.CertFile, identity.KeyFile} {
		if _, err := os.Stat(path); err == nil {
			existing++
		} else if !os.IsNotExist(err) {
			return Identity{}, err
		}
	}
	if existing > 0 {
		if existing != 4 {
			return Identity{}, fmt.Errorf("TLS identity in %s is incomplete", dir)
		}
		serverPair, err := tls.LoadX509KeyPair(identity.CertFile, identity.KeyFile)
		if err != nil {
			return Identity{}, fmt.Errorf("load existing TLS server identity: %w", err)
		}
		caPair, err := tls.LoadX509KeyPair(identity.CACertFile, identity.CAKeyFile)
		if err != nil {
			return Identity{}, fmt.Errorf("load existing TLS certificate authority: %w", err)
		}
		leaf, err := x509.ParseCertificate(serverPair.Certificate[0])
		if err != nil {
			return Identity{}, fmt.Errorf("parse existing TLS server certificate: %w", err)
		}
		ca, err := x509.ParseCertificate(caPair.Certificate[0])
		if err != nil {
			return Identity{}, fmt.Errorf("parse existing TLS certificate authority: %w", err)
		}
		roots := x509.NewCertPool()
		roots.AddCert(ca)
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots}); err != nil {
			return Identity{}, fmt.Errorf("verify existing TLS server certificate against local CA: %w", err)
		}
		coversHosts := true
		for _, host := range hosts {
			host = strings.TrimSpace(host)
			if host != "" {
				if err := leaf.VerifyHostname(host); err != nil {
					coversHosts = false
					break
				}
			}
		}
		if exact && len(leaf.DNSNames)+len(leaf.IPAddresses) != len(dnsNames)+len(ipAddresses) {
			coversHosts = false
		}
		if !coversHosts {
			serverSigner, ok := serverPair.PrivateKey.(crypto.Signer)
			if !ok {
				return Identity{}, fmt.Errorf("existing TLS server key cannot sign certificates")
			}
			caSigner, ok := caPair.PrivateKey.(crypto.Signer)
			if !ok {
				return Identity{}, fmt.Errorf("existing TLS certificate authority key cannot sign certificates")
			}
			now := time.Now().UTC()
			template := serverCertificateTemplate(now, dnsNames, ipAddresses)
			serverDER, err := x509.CreateCertificate(rand.Reader, template, ca, serverSigner.Public(), caSigner)
			if err != nil {
				return Identity{}, fmt.Errorf("reissue TLS server certificate: %w", err)
			}
			if err := replacePEM(identity.CertFile, "CERTIFICATE", serverDER, 0o644); err != nil {
				return Identity{}, fmt.Errorf("replace TLS server certificate: %w", err)
			}
		}
		return identity, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Identity{}, err
	}
	now := time.Now().UTC()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	caTemplate := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Regstair Local CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return Identity{}, err
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	serverTemplate := serverCertificateTemplate(now, dnsNames, ipAddresses)
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		return Identity{}, err
	}
	caKeyDER, err := x509.MarshalPKCS8PrivateKey(caKey)
	if err != nil {
		return Identity{}, err
	}
	serverKeyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		return Identity{}, err
	}
	files := []struct {
		path, kind string
		der        []byte
		mode       os.FileMode
	}{{identity.CACertFile, "CERTIFICATE", caDER, 0o644}, {identity.CAKeyFile, "PRIVATE KEY", caKeyDER, 0o600}, {identity.CertFile, "CERTIFICATE", serverDER, 0o644}, {identity.KeyFile, "PRIVATE KEY", serverKeyDER, 0o600}}
	for _, file := range files {
		if err := writeExclusivePEM(file.path, file.kind, file.der, file.mode); err != nil {
			return Identity{}, err
		}
	}
	return identity, nil
}

func serverCertificateTemplate(now time.Time, dnsNames []string, ipAddresses []net.IP) *x509.Certificate {
	return &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: firstName(dnsNames, ipAddresses)}, DNSNames: dnsNames, IPAddresses: ipAddresses, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
}

func normalizeHosts(hosts []string) ([]string, []net.IP) {
	seen := map[string]bool{}
	var dns []string
	var ips []net.IP
	for _, raw := range hosts {
		host := strings.TrimSpace(raw)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		if ip := net.ParseIP(host); ip != nil {
			ips = append(ips, ip)
		} else {
			dns = append(dns, host)
		}
	}
	return dns, ips
}

// DiscoverHostAddresses returns globally scoped addresses on active Linux host interfaces.
func DiscoverHostAddresses() ([]string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list host network interfaces: %w", err)
	}
	var addresses []net.Addr
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 || ignoredLinuxInterface(networkInterface.Name) {
			continue
		}
		interfaceAddresses, err := networkInterface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("list addresses for interface %s: %w", networkInterface.Name, err)
		}
		addresses = append(addresses, interfaceAddresses...)
	}
	return usableAddressStrings(addresses), nil
}

func ignoredLinuxInterface(name string) bool {
	return name == "docker0" || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "virbr")
}

func usableAddressStrings(addresses []net.Addr) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err != nil {
			ip = net.ParseIP(address.String())
		}
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			continue
		}
		value := ip.String()
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func firstName(dns []string, ips []net.IP) string {
	if len(dns) > 0 {
		return dns[0]
	}
	return ips[0].String()
}

func serial() *big.Int {
	value, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	return value
}

func writeExclusivePEM(path, kind string, der []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err := pem.Encode(file, &pem.Block{Type: kind, Bytes: der}); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func replacePEM(path, kind string, der []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".regstair-cert-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := pem.Encode(temporary, &pem.Block{Type: kind, Bytes: der}); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
