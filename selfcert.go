package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

// persistentCertificate returns a stable self-signed TLS certificate, creating
// it on first use and persisting it under the config dir. Reusing the same cert
// across restarts keeps krokodyl's SHA-256 fingerprint stable — otherwise every
// launch looks like a brand-new device to LocalSend and to peers (the "same
// machine shows up several times" churn). Falls back to an in-memory cert if
// persistence fails, so it never blocks startup.
func persistentCertificate() (tls.Certificate, error) {
	path := selfCertPath()
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if cert, err := tls.X509KeyPair(data, data); err == nil {
				if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil && time.Now().Before(leaf.NotAfter) {
					return cert, nil
				}
			}
		}
	}
	cert, pemBytes, err := newSelfSignedPEM()
	if err != nil {
		return tls.Certificate{}, err
	}
	if path != "" {
		if err := writeSelfCert(path, pemBytes); err != nil {
			logrus.WithError(err).Warn("could not persist self certificate; krokodyl may appear as a new device each launch")
		}
	}
	return cert, nil
}

func selfCertPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "krokodyl", "self-cert.pem")
}

// newSelfSignedPEM generates an ECDSA self-signed cert valid for 10 years and
// returns it both as a tls.Certificate and as combined CERTIFICATE + EC PRIVATE
// KEY PEM bytes for persistence.
func newSelfSignedPEM() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "krokodyl"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := pem.Encode(&buf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, buf.Bytes(), nil
}

// writeSelfCert persists the cert PEM atomically at 0o600 (it holds a private key).
func writeSelfCert(path string, pemBytes []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pemBytes, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
