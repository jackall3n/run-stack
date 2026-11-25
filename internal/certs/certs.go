// Package certs is the machine's own certificate authority. The proxy signs a certificate for
// each host it serves with it, so trusting this one root makes every stack's HTTPS valid.
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/jackall3n/run-stack/internal/home"
)

type Authority struct {
	// Root is the certificate to trust.
	Root *x509.Certificate
	key  *ecdsa.PrivateKey

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

func serial() *big.Int {
	number, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))

	return number
}

// Load reads the authority from ~/.run-stack/ca, creating it the first time.
func Load() (*Authority, error) {
	certPEM, certErr := os.ReadFile(home.CACert())
	keyPEM, keyErr := os.ReadFile(home.CAKey())

	if certErr == nil && keyErr == nil {
		certBlock, _ := pem.Decode(certPEM)
		keyBlock, _ := pem.Decode(keyPEM)

		if certBlock == nil || keyBlock == nil {
			return nil, fmt.Errorf("%s does not hold a PEM certificate and key", filepath.Dir(home.CAKey()))
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return nil, err
		}

		key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, err
		}

		return &Authority{Root: cert, key: key, leaves: map[string]*tls.Certificate{}}, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	hostname, _ := os.Hostname()
	template := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{Organization: []string{"run-stack"}, CommonName: "run-stack local CA (" + hostname + ")"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(home.CAKey()), 0o700); err != nil {
		return nil, err
	}

	// The key can sign a certificate for any site, so only this user may read it.
	if err := os.WriteFile(home.CAKey(), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}

	if err := os.WriteFile(home.CACert(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}

	return &Authority{Root: cert, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

// Certificate returns a certificate for whichever host the client asked for, signing one the
// first time that host is seen. It is a tls.Config.GetCertificate.
func (authority *Authority) Certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := hello.ServerName

	if host == "" {
		host = "localhost"
	}

	authority.mu.Lock()
	defer authority.mu.Unlock()

	if leaf := authority.leaves[host]; leaf != nil && time.Now().Before(leaf.Leaf.NotAfter.Add(-24*time.Hour)) {
		return leaf, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{Organization: []string{"run-stack"}, CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		// Well inside the 398 days Apple accepts; a new one is signed when this runs out.
		NotAfter:    time.Now().AddDate(0, 0, 90),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	der, err := x509.CreateCertificate(rand.Reader, template, authority.Root, &key.PublicKey, authority.key)
	if err != nil {
		return nil, err
	}

	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}

	leaf := &tls.Certificate{Certificate: [][]byte{der, authority.Root.Raw}, PrivateKey: key, Leaf: parsed}
	authority.leaves[host] = leaf

	return leaf, nil
}

// Trusted reports whether the system accepts the authority. Only macOS is checked; elsewhere the
// answer is unknown, reported as true so nothing nags.
func Trusted() bool {
	if runtime.GOOS != "darwin" {
		return true
	}

	if _, err := os.Stat(home.CACert()); err != nil {
		return false
	}

	return exec.Command("security", "verify-cert", "-c", home.CACert()).Run() == nil
}

// Trust adds the authority to the user's login keychain. macOS asks for the password.
func Trust() error {
	if _, err := Load(); err != nil {
		return err
	}

	if runtime.GOOS != "darwin" {
		return fmt.Errorf("add %s to your system's trusted certificate authorities by hand", home.CACert())
	}

	if Trusted() {
		return nil
	}

	userHome, _ := os.UserHomeDir()
	cmd := exec.Command("security", "add-trusted-cert", "-r", "trustRoot", "-k", filepath.Join(userHome, "Library/Keychains/login.keychain-db"), home.CACert())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	if err := cmd.Run(); err != nil {
		return errors.New("the certificate authority was not trusted")
	}

	return nil
}
