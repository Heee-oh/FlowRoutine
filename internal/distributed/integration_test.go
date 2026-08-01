package distributed

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"flowroutine/internal/bridge"
	"flowroutine/internal/engine"
)

func TestWorkerRequiresVerifiedMutualTLS(t *testing.T) {
	worker, err := NewWorkerServer("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, statusPath, nil)
	response := httptest.NewRecorder()
	worker.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("plain request got HTTP %d, want 401", response.Code)
	}

	serverTLS, clientTLS, rootsOnly := testMutualTLS(t)
	server := startWorkerTestServer(t, worker, serverTLS)
	defer server.Close()
	unauthenticated := &http.Client{
		Transport: &http.Transport{TLSClientConfig: rootsOnly},
		Timeout:   time.Second,
	}
	if _, err := unauthenticated.Get(server.URL + statusPath); err == nil {
		t.Fatal("worker accepted a TLS client without a certificate")
	}

	client, err := NewWorkerClient(WorkerTarget{ID: "worker-1", URL: server.URL, TLSConfig: clientTLS})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Status(context.Background(), ""); err != nil {
		t.Fatalf("mutually authenticated status failed: %v", err)
	}
}

func TestCoordinatorRunsSecretBoundPlanAndProducesReportSchema(t *testing.T) {
	var authorized atomic.Uint64
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer runtime-secret" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		authorized.Add(1)
		response.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	serverTLS, clientTLS, _ := testMutualTLS(t)
	worker, err := NewWorkerServer("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	server := startWorkerTestServer(t, worker, serverTLS)
	defer server.Close()
	coordinator, err := NewCoordinator(
		[]WorkerTarget{{ID: "worker-1", URL: server.URL, TLSConfig: clientTLS}},
		WithStartLead(250*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()

	plan := NewExecutionPlan("secret-plan", 1, engine.Config{
		URL:             target.URL,
		VirtualUsers:    2,
		Duration:        150 * time.Millisecond,
		RequestTimeout:  time.Second,
		MaxConnsPerHost: 8,
		ScenarioSteps: []engine.ScenarioStep{{
			ID:      "authorized-request",
			Name:    "authorized request",
			Kind:    engine.StepRequest,
			URL:     target.URL,
			Headers: []engine.Header{{Name: "Authorization", Value: "Bearer {{SECRET_TOKEN}}"}},
		}},
	})
	run, err := coordinator.Start(context.Background(), plan, map[string]string{"SECRET_TOKEN": "runtime-secret"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitForTerminal(t, run, 3*time.Second)
	if result.Partial || result.Snapshot.TotalRequests == 0 || result.Snapshot.FailedRequests != 0 {
		t.Fatalf("unexpected distributed result: %+v", result)
	}
	if authorized.Load() != result.Snapshot.TotalRequests {
		t.Fatalf("authorized requests=%d, aggregate total=%d", authorized.Load(), result.Snapshot.TotalRequests)
	}
	if len(result.RequestSteps) != 1 || result.RequestSteps[0].TotalRequests != result.Snapshot.TotalRequests {
		t.Fatalf("request-step aggregate does not match run: %+v", result.RequestSteps)
	}
	batch := bridge.BuildMetricsBatch(
		engine.Snapshot{At: result.Snapshot.StartedAt},
		result.Snapshot,
		false,
		result.Snapshot.At,
		result.RequestSteps,
	)
	if batch.Total != result.Snapshot.TotalRequests || len(batch.StepMetrics) != 1 {
		t.Fatalf("distributed result did not use the report schema: %+v", batch)
	}
}

func TestRunRetainsLastGoodMetricsWhenWorkerBecomesUnavailable(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	serverTLS, clientTLS, _ := testMutualTLS(t)

	servers := make([]*httptest.Server, 2)
	targets := make([]WorkerTarget, 2)
	for index := range servers {
		id := "worker-" + string(rune('1'+index))
		worker, err := NewWorkerServer(id)
		if err != nil {
			t.Fatal(err)
		}
		servers[index] = startWorkerTestServer(t, worker, serverTLS.Clone())
		targets[index] = WorkerTarget{ID: id, URL: servers[index].URL, TLSConfig: clientTLS.Clone()}
	}
	defer servers[0].Close()
	coordinator, err := NewCoordinator(targets, WithStartLead(250*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()
	run, err := coordinator.Start(context.Background(), NewExecutionPlan("partial-plan", 1, engine.Config{
		URL:             target.URL,
		VirtualUsers:    4,
		Duration:        time.Second,
		RequestTimeout:  time.Second,
		MaxConnsPerHost: 8,
	}), nil)
	if err != nil {
		t.Fatal(err)
	}

	baseline := waitForRequests(t, run, 3*time.Second)
	if baseline.Workers[0].StartedUnixNano == 0 || baseline.Workers[1].StartedUnixNano == 0 {
		t.Fatalf("worker start times were not reported: %+v", baseline.Workers)
	}
	startDelta := time.Duration(baseline.Workers[0].StartedUnixNano - baseline.Workers[1].StartedUnixNano)
	if startDelta < 0 {
		startDelta = -startDelta
	}
	if startDelta > 100*time.Millisecond {
		t.Fatalf("worker starts differed by %s", startDelta)
	}
	coordinator.clients[1].transport.CloseIdleConnections()
	servers[1].Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	partial := run.Snapshot(ctx)
	if !partial.Partial || !partial.Workers[1].Stale || partial.Workers[1].Reachable {
		t.Fatalf("worker failure was not visible: %+v", partial.Workers)
	}
	if partial.Snapshot.TotalRequests < baseline.Snapshot.TotalRequests {
		t.Fatalf("aggregate regressed from %d to %d", baseline.Snapshot.TotalRequests, partial.Snapshot.TotalRequests)
	}
	_, _ = run.Stop(ctx)
}

func waitForTerminal(t *testing.T, run *Run, timeout time.Duration) AggregateResult {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		result := run.Snapshot(context.Background())
		if result.AllTerminal {
			return result
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("distributed run did not finish before timeout")
	return AggregateResult{}
}

func waitForRequests(t *testing.T, run *Run, timeout time.Duration) AggregateResult {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		result := run.Snapshot(context.Background())
		allStarted := true
		for _, worker := range result.Workers {
			allStarted = allStarted && worker.StartedUnixNano != 0
		}
		if result.Snapshot.TotalRequests > 0 && allStarted {
			return result
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("distributed run did not produce requests before timeout")
	return AggregateResult{}
}

func startWorkerTestServer(t *testing.T, worker *WorkerServer, tlsConfig *tls.Config) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(worker)
	server.TLS = tlsConfig
	server.StartTLS()
	return server
}

func testMutualTLS(t *testing.T) (*tls.Config, *tls.Config, *tls.Config) {
	t.Helper()
	caCertificate, caKey := testCertificateAuthority(t)
	workerCertificate := testLeafCertificate(t, caCertificate, caKey, true)
	coordinatorCertificate := testLeafCertificate(t, caCertificate, caKey, false)
	pool := x509.NewCertPool()
	pool.AddCert(caCertificate)
	serverTLS, err := ServerTLSConfig(workerCertificate, pool)
	if err != nil {
		t.Fatal(err)
	}
	clientTLS, err := ClientTLSConfig(coordinatorCertificate, pool, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	rootsOnly := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, ServerName: "127.0.0.1"}
	return serverTLS, clientTLS, rootsOnly
}

func testCertificateAuthority(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "FlowRoutine test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key
}

func testLeafCertificate(
	t *testing.T,
	caCertificate *x509.Certificate,
	caKey *ecdsa.PrivateKey,
	server bool,
) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "FlowRoutine test peer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.DNSNames = []string{"localhost"}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCertificate, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
