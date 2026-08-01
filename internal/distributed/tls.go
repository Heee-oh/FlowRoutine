package distributed

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

func LoadServerTLSConfig(certificateFile string, keyFile string, clientCAFile string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load worker certificate: %w", err)
	}
	clientCAs, err := loadCertificatePool(clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("load coordinator CA: %w", err)
	}
	return ServerTLSConfig(certificate, clientCAs)
}

func LoadClientTLSConfig(certificateFile string, keyFile string, workerCAFile string, serverName string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load coordinator certificate: %w", err)
	}
	roots, err := loadCertificatePool(workerCAFile)
	if err != nil {
		return nil, fmt.Errorf("load worker CA: %w", err)
	}
	return ClientTLSConfig(certificate, roots, serverName)
}

func ServerTLSConfig(certificate tls.Certificate, clientCAs *x509.CertPool) (*tls.Config, error) {
	if len(certificate.Certificate) == 0 {
		return nil, errors.New("worker TLS certificate is required")
	}
	if clientCAs == nil || len(clientCAs.Subjects()) == 0 {
		return nil, errors.New("coordinator client CA is required")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		NextProtos:   []string{"h2", "http/1.1"},
	}, nil
}

func ClientTLSConfig(certificate tls.Certificate, roots *x509.CertPool, serverName string) (*tls.Config, error) {
	if len(certificate.Certificate) == 0 {
		return nil, errors.New("coordinator TLS certificate is required")
	}
	if roots == nil || len(roots.Subjects()) == 0 {
		return nil, errors.New("worker CA is required")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		RootCAs:      roots,
		ServerName:   serverName,
		NextProtos:   []string{"h2", "http/1.1"},
	}, nil
}

func loadCertificatePool(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, errors.New("certificate authority file is required")
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("certificate authority file contains no valid certificates")
	}
	return pool, nil
}
