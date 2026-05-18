package engine

import (
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"syscall"

	"github.com/valyala/fasthttp"
)

type FailureKind uint8

const (
	FailureOther FailureKind = iota
	FailureTimeout
	FailureDNS
	FailureTLS
	FailureConnectionRefused
)

func ClassifyFailure(err error) FailureKind {
	if err == nil {
		return FailureOther
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return FailureDNS
	}
	if errors.Is(err, fasthttp.ErrTLSHandshakeTimeout) {
		return FailureTLS
	}
	var recordHeaderErr tls.RecordHeaderError
	if errors.As(err, &recordHeaderErr) {
		return FailureTLS
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return FailureTLS
	}
	if errors.Is(err, fasthttp.ErrTimeout) {
		return FailureTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return FailureTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return FailureConnectionRefused
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "connection refused"):
		return FailureConnectionRefused
	case strings.Contains(message, "no such host"):
		return FailureDNS
	case strings.Contains(message, "tls") || strings.Contains(message, "certificate") || strings.Contains(message, "x509"):
		return FailureTLS
	case strings.Contains(message, "timeout") || strings.Contains(message, "deadline exceeded"):
		return FailureTimeout
	default:
		return FailureOther
	}
}
