package engine

import (
	"crypto/tls"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/valyala/fasthttp"
)

func TestClassifyFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want FailureKind
	}{
		{name: "timeout", err: fasthttp.ErrTimeout, want: FailureTimeout},
		{name: "dns", err: &net.DNSError{Err: "no such host", Name: "example.invalid"}, want: FailureDNS},
		{name: "tls", err: tls.RecordHeaderError{}, want: FailureTLS},
		{name: "connection refused", err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}, want: FailureConnectionRefused},
		{name: "other", err: errors.New("unexpected eof"), want: FailureOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyFailure(tt.err); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}
