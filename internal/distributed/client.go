package distributed

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultControlTimeout = 10 * time.Second

type WorkerTarget struct {
	ID        string
	URL       string
	TLSConfig *tls.Config
}

type WorkerClient struct {
	id         string
	baseURL    *url.URL
	httpClient *http.Client
	transport  *http.Transport
}

func NewWorkerClient(target WorkerTarget) (*WorkerClient, error) {
	if err := validateIdentifier("worker id", target.ID); err != nil {
		return nil, err
	}
	baseURL, err := url.Parse(target.URL)
	if err != nil {
		return nil, fmt.Errorf("parse worker URL: %w", err)
	}
	if baseURL.Scheme != "https" || baseURL.Host == "" {
		return nil, errors.New("worker URL must use https with a host")
	}
	if baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("worker URL must not contain credentials, a query, or a fragment")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	if target.TLSConfig == nil {
		return nil, errors.New("worker mutual TLS config is required")
	}
	tlsConfig := target.TLSConfig.Clone()
	if tlsConfig.InsecureSkipVerify {
		return nil, errors.New("worker TLS verification cannot be disabled")
	}
	if len(tlsConfig.Certificates) == 0 {
		return nil, errors.New("coordinator client certificate is required")
	}
	if tlsConfig.RootCAs == nil || len(tlsConfig.RootCAs.Subjects()) == 0 {
		return nil, errors.New("worker certificate authority is required")
	}
	if tlsConfig.MinVersion == 0 || tlsConfig.MinVersion < tls.VersionTLS13 {
		tlsConfig.MinVersion = tls.VersionTLS13
	}
	transport := &http.Transport{
		TLSClientConfig:     tlsConfig,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &WorkerClient{
		id:        target.ID,
		baseURL:   baseURL,
		transport: transport,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   DefaultControlTimeout,
		},
	}, nil
}

func (client *WorkerClient) ID() string {
	return client.id
}

func (client *WorkerClient) Close() {
	client.transport.CloseIdleConnections()
}

func (client *WorkerClient) Status(ctx context.Context, runID string) (StatusResponse, error) {
	var response StatusResponse
	query := url.Values{}
	if runID != "" {
		query.Set("runId", runID)
	}
	if err := client.get(ctx, statusPath, query, &response); err != nil {
		return StatusResponse{}, err
	}
	if err := client.validateStatus(response); err != nil {
		return StatusResponse{}, err
	}
	return response, nil
}

func (client *WorkerClient) Prepare(ctx context.Context, request PrepareRequest) (PrepareResponse, error) {
	var response PrepareResponse
	if err := client.post(ctx, preparePath, request, &response); err != nil {
		return PrepareResponse{}, err
	}
	if response.ProtocolVersion != ProtocolVersion {
		return PrepareResponse{}, errors.New("worker returned an unsupported protocol version")
	}
	if response.WorkerID != client.id || response.RunID != request.RunID {
		return PrepareResponse{}, errors.New("worker returned a mismatched identity or run")
	}
	return response, nil
}

func (client *WorkerClient) Start(ctx context.Context, request StartRequest) (StatusResponse, error) {
	var response StatusResponse
	if err := client.post(ctx, startPath, request, &response); err != nil {
		return StatusResponse{}, err
	}
	if err := client.validateStatus(response); err != nil {
		return StatusResponse{}, err
	}
	if response.RunID != request.RunID {
		return StatusResponse{}, errors.New("worker returned a mismatched run")
	}
	return response, nil
}

func (client *WorkerClient) Snapshot(ctx context.Context, runID string) (SnapshotResponse, error) {
	var response SnapshotResponse
	query := url.Values{"runId": []string{runID}}
	if err := client.get(ctx, snapshotPath, query, &response); err != nil {
		return SnapshotResponse{}, err
	}
	if err := client.validateStatus(response.Status); err != nil {
		return SnapshotResponse{}, err
	}
	if response.Status.RunID != runID {
		return SnapshotResponse{}, errors.New("worker returned a mismatched run")
	}
	return response, nil
}

func (client *WorkerClient) Stop(ctx context.Context, request StopRequest) (StatusResponse, error) {
	var response StatusResponse
	if err := client.post(ctx, stopPath, request, &response); err != nil {
		return StatusResponse{}, err
	}
	if err := client.validateStatus(response); err != nil {
		return StatusResponse{}, err
	}
	if response.RunID != request.RunID {
		return StatusResponse{}, errors.New("worker returned a mismatched run")
	}
	return response, nil
}

func (client *WorkerClient) validateStatus(status StatusResponse) error {
	if status.ProtocolVersion != ProtocolVersion {
		return errors.New("worker returned an unsupported protocol version")
	}
	if status.WorkerID != client.id {
		return errors.New("worker returned a mismatched identity")
	}
	return nil
}

func (client *WorkerClient) get(ctx context.Context, path string, query url.Values, target any) error {
	endpoint := *client.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	return client.do(request, target)
}

func (client *WorkerClient) post(ctx context.Context, path string, value any, target any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode control request")
	}
	if len(payload) > MaxControlBytes {
		return errors.New("control request exceeds the size limit")
	}
	endpoint := *client.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	return client.do(request, target)
}

func (client *WorkerClient) do(request *http.Request, target any) error {
	response, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, MaxControlBytes+1)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var body struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(limited).Decode(&body); err != nil || body.Error == "" {
			return fmt.Errorf("worker returned HTTP %d", response.StatusCode)
		}
		return fmt.Errorf("worker returned HTTP %d: %s", response.StatusCode, body.Error)
	}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode worker response: %w", err)
	}
	return nil
}
