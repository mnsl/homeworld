package authorities

import (
	"bytes"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"keyserver/verifier"
	"util/wraputil"
	"util/certutil"
)

type TLSAuthority struct {
	// TODO: also support ECDSA or other newer algorithms
	key         *rsa.PrivateKey
	cert        *x509.Certificate
	certEncoded []byte
}

func (t *TLSAuthority) Equal(authority *TLSAuthority) bool {
	return bytes.Equal(t.cert.Raw, authority.cert.Raw)
}

func LoadTLSAuthority(keydata []byte, certdata []byte) (Authority, error) {
	privkey, err := wraputil.LoadRSAKeyFromPEM(keydata)
	if err != nil {
		return nil, err
	}

	cert, err := wraputil.LoadX509CertFromPEM(certdata)
	if err != nil {
		return nil, err
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if cert.PublicKeyAlgorithm != x509.RSA || !ok {
		return nil, errors.New("expected RSA public key in certificate")
	}
	if pub.N.Cmp(privkey.N) != 0 {
		return nil, errors.New("mismatched RSA public and private keys")
	}

	return &TLSAuthority{key: privkey, cert: cert, certEncoded: certdata}, nil
}

func (t *TLSAuthority) ToCertPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(t.cert)
	return pool
}

func (t *TLSAuthority) GetPublicKey() []byte {
	return t.certEncoded
}

func (t *TLSAuthority) ToHTTPSCert() tls.Certificate {
	return tls.Certificate{Certificate: [][]byte{t.cert.Raw}, PrivateKey: t.key}
}

// Ensure *TLSAuthority implements Verifier
var _ verifier.Verifier = (*TLSAuthority)(nil)

func (t *TLSAuthority) HasAttempt(request *http.Request) bool {
	return request.TLS != nil && len(request.TLS.VerifiedChains) > 0 && len(request.TLS.VerifiedChains[0]) > 0
}

func (t *TLSAuthority) Verify(request *http.Request) (string, error) {
	if !t.HasAttempt(request) {
		return "", fmt.Errorf("Client certificate must be present")
	}
	firstCert := request.TLS.VerifiedChains[0][0]
	chains, err := firstCert.Verify(x509.VerifyOptions{
		Roots:     t.ToCertPool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if len(chains) == 0 || err != nil {
		return "", fmt.Errorf("Certificate not valid under this authority: %s", err)
	}
	principal := firstCert.Subject.CommonName
	return principal, nil
}

func (t *TLSAuthority) Sign(request string, ishost bool, lifespan time.Duration, commonname string, names []string) (string, error) {
	csr, err := wraputil.LoadX509CSRFromPEM([]byte(request))
	if err != nil {
		return "", err
	}
	err = csr.CheckSignature()
	if err != nil {
		return "", err
	}

	issue_at := time.Now()

	dns_names, IPs := partitionDNSNamesAndIPs(names)

	certTemplate := &x509.Certificate{
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},

		BasicConstraintsValid: true,
		IsCA:           false,
		MaxPathLen:     0,
		MaxPathLenZero: true,

		NotBefore: issue_at,
		NotAfter:  issue_at.Add(lifespan),

		Subject:     pkix.Name{CommonName: commonname},
		DNSNames:    dns_names,
		IPAddresses: IPs,
	}

	if ishost {
		certTemplate.ExtKeyUsage = append(certTemplate.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
	}

	signed_cert, err := certutil.FinishCertificate(certTemplate, t.cert, csr.PublicKey, t.key)
	if err != nil {
		return "", err
	}
	return string(signed_cert), nil
}

func partitionDNSNamesAndIPs(names []string) ([]string, []net.IP) {
	dnses := make([]string, 0)
	ips := make([]net.IP, 0)
	for _, name := range names {
		ip := net.ParseIP(name)
		if ip == nil {
			dnses = append(dnses, name)
		} else {
			ips = append(ips, ip)
		}
	}
	return dnses, ips
}
