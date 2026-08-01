package bridge

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestImportOpenAPIFetchesAndValidatesJSON(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Accept"); !strings.Contains(got, "application/json") {
			t.Fatalf("Accept header %q does not include application/json", got)
		}
		return jsonResponse(http.StatusOK, `{
			"openapi": "3.0.1",
			"info": {"title": "Demo API", "version": "1.0.0"},
			"servers": [{"url": "http://localhost:8080", "description": "Local"}],
			"components": {
				"parameters": {
					"PageParam": {
						"name": "page",
						"in": "query",
						"schema": {"type": "integer", "default": 1}
					}
				},
				"securitySchemes": {
					"jwtAuth": {"type": "http", "scheme": "bearer", "bearerFormat": "JWT"},
					"apiKeyAuth": {"type": "apiKey", "in": "header", "name": "X-Demo-Key"},
					"sessionAuth": {"type": "apiKey", "in": "cookie", "name": "SESSION"}
				},
				"schemas": {
					"OrderRequest": {
						"type": "object",
						"properties": {
							"customerEmail": {"type": "string", "format": "email"},
							"quantity": {"type": "integer"},
							"expedited": {"type": "boolean"},
							"items": {"type": "array", "items": {"type": "string"}},
							"status": {"type": "string", "enum": ["PENDING", "DONE"]},
							"metadata": {
								"type": "object",
								"properties": {
									"note": {"type": "string"}
								}
							}
						}
					}
				}
			},
			"paths": {
				"/orders": {
					"parameters": [{"name": "trace", "in": "header"}],
					"post": {
						"summary": "Create order",
						"operationId": "createOrder",
						"tags": ["orders"],
						"deprecated": true,
						"security": [{"jwtAuth": []}],
						"requestBody": {
							"content": {
								"application/json": {
									"schema": {"$ref": "#/components/schemas/OrderRequest"}
								}
							}
						}
					}
				},
				"/users": {
					"parameters": [{"$ref": "#/components/parameters/PageParam"}],
					"get": {
						"summary": "List users",
						"operationId": "listUsers",
						"tags": ["users"],
						"security": [{"apiKeyAuth": []}],
						"parameters": [
							{"name": "size", "in": "query", "schema": {"type": "integer", "example": 20}},
							{"name": "session", "in": "cookie", "schema": {"type": "string"}}
						]
					},
					"post": {"description": "Create user", "security": [{"sessionAuth": []}]}
				}
			}
		}`), nil
	})

	got, err := NewController(nil).ImportOpenAPI(context.Background(), "http://localhost:8080/v3/api-docs")
	if err != nil {
		t.Fatalf("ImportOpenAPI returned error: %v", err)
	}
	if got.SourceURL != "http://localhost:8080/v3/api-docs" {
		t.Fatalf("SourceURL = %q", got.SourceURL)
	}
	if got.OpenAPI != "3.0.1" {
		t.Fatalf("OpenAPI = %q, want 3.0.1", got.OpenAPI)
	}
	if got.Title != "Demo API" {
		t.Fatalf("Title = %q, want Demo API", got.Title)
	}
	if got.Version != "1.0.0" {
		t.Fatalf("Version = %q, want 1.0.0", got.Version)
	}
	if !strings.Contains(string(got.RawJSON), `"/users"`) {
		t.Fatalf("RawJSON does not contain paths: %s", got.RawJSON)
	}
	if len(got.Servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(got.Servers))
	}
	if got.Servers[0].URL != "http://localhost:8080" {
		t.Fatalf("server URL = %q", got.Servers[0].URL)
	}
	if len(got.Endpoints) != 3 {
		t.Fatalf("got %d endpoints, want 3: %+v", len(got.Endpoints), got.Endpoints)
	}
	assertEndpoint(t, got.Endpoints[0], OpenAPIEndpoint{
		Method:      "POST",
		Path:        "/orders",
		Summary:     "Create order",
		OperationID: "createOrder",
		Tags:        []string{"orders"},
		ServerURL:   "http://localhost:8080",
		Deprecated:  true,
		Auth:        OpenAPIAuth{Type: "bearer"},
		Parameters: []OpenAPIParameter{
			{Name: "trace", In: "header", Sample: "string"},
		},
		BodySample: strings.Join([]string{
			"{",
			`  "customerEmail": "user@example.com",`,
			`  "expedited": false,`,
			`  "items": [],`,
			`  "metadata": {`,
			`    "note": "string"`,
			`  },`,
			`  "quantity": 0,`,
			`  "status": "PENDING"`,
			"}",
		}, "\n"),
	})
	assertEndpoint(t, got.Endpoints[1], OpenAPIEndpoint{
		Method:      "GET",
		Path:        "/users",
		Summary:     "List users",
		OperationID: "listUsers",
		Tags:        []string{"users"},
		ServerURL:   "http://localhost:8080",
		Auth:        OpenAPIAuth{Type: "apiKey", Name: "X-Demo-Key"},
		Parameters: []OpenAPIParameter{
			{Name: "page", In: "query", Sample: "1"},
			{Name: "size", In: "query", Sample: "20"},
			{Name: "session", In: "cookie", Sample: "string"},
		},
	})
	assertEndpoint(t, got.Endpoints[2], OpenAPIEndpoint{
		Method:    "POST",
		Path:      "/users",
		Summary:   "Create user",
		ServerURL: "http://localhost:8080",
		Auth:      OpenAPIAuth{Type: "cookie", Name: "SESSION"},
		Parameters: []OpenAPIParameter{
			{Name: "page", In: "query", Sample: "1"},
		},
	})
}

func TestImportOpenAPIUsesDocumentSecurityAndOperationOverride(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"openapi": "3.0.1",
			"components": {
				"securitySchemes": {
					"jwtAuth": {"type": "http", "scheme": "bearer"}
				}
			},
			"security": [{"jwtAuth": []}],
			"paths": {
				"/private": {"get": {"summary": "Private"}},
				"/public": {"get": {"summary": "Public", "security": []}}
			}
		}`), nil
	})

	got, err := NewController(nil).ImportOpenAPI(context.Background(), "http://localhost:8080/v3/api-docs")
	if err != nil {
		t.Fatalf("ImportOpenAPI returned error: %v", err)
	}
	if got.Endpoints[0].Path != "/private" || got.Endpoints[0].Auth.Type != "bearer" {
		t.Fatalf("private endpoint auth = %+v", got.Endpoints[0])
	}
	if got.Endpoints[1].Path != "/public" || got.Endpoints[1].Auth.Type != "none" {
		t.Fatalf("public endpoint auth = %+v", got.Endpoints[1])
	}
}

func TestImportOpenAPIParsesSwaggerSecurityDefinitions(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"swagger": "2.0",
			"securityDefinitions": {
				"apiKeyAuth": {"type": "apiKey", "in": "header", "name": "X-Api-Key"}
			},
			"security": [{"apiKeyAuth": []}],
			"paths": {"/private": {"get": {"summary": "Private"}}}
		}`), nil
	})

	got, err := NewController(nil).ImportOpenAPI(context.Background(), "http://localhost:8080/v2/api-docs")
	if err != nil {
		t.Fatalf("ImportOpenAPI returned error: %v", err)
	}
	if len(got.Endpoints) != 1 || got.Endpoints[0].Auth != (OpenAPIAuth{Type: "apiKey", Name: "X-Api-Key"}) {
		t.Fatalf("endpoints = %+v", got.Endpoints)
	}
}

func TestImportOpenAPIParsesPathAndQueryParameters(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"openapi": "3.0.1",
			"paths": {
				"/users/{id}": {
					"parameters": [
						{"name": "id", "in": "path", "required": true, "schema": {"type": "string", "format": "uuid"}}
					],
					"get": {
						"parameters": [
							{"name": "includePosts", "in": "query", "schema": {"type": "boolean"}},
							{"name": "sort", "in": "query", "schema": {"type": "string", "enum": ["name", "createdAt"]}}
						]
					}
				}
			}
		}`), nil
	})

	got, err := NewController(nil).ImportOpenAPI(context.Background(), "http://localhost:8080/v3/api-docs")
	if err != nil {
		t.Fatalf("ImportOpenAPI returned error: %v", err)
	}
	if len(got.Endpoints) != 1 {
		t.Fatalf("got %d endpoints, want 1", len(got.Endpoints))
	}
	want := []OpenAPIParameter{
		{Name: "id", In: "path", Required: true, Sample: "00000000-0000-0000-0000-000000000000"},
		{Name: "includePosts", In: "query", Sample: "false"},
		{Name: "sort", In: "query", Sample: "name"},
	}
	assertParameters(t, got.Endpoints[0].Parameters, want)
}

func TestImportOpenAPIRejectsInvalidURLs(t *testing.T) {
	tests := []string{
		"",
		"file:///tmp/openapi.json",
		"http://user:pass@example.com/v3/api-docs",
		"http:///v3/api-docs",
	}

	controller := NewController(nil)
	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			if _, err := controller.ImportOpenAPI(context.Background(), rawURL); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestImportOpenAPIRejectsNonOpenAPIJSON(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"hello":"world"}`), nil
	})

	if _, err := NewController(nil).ImportOpenAPI(context.Background(), "http://localhost:8080/v3/api-docs"); err == nil {
		t.Fatal("expected error")
	}
}

func TestImportOpenAPIRejectsHTTPError(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotFound, `{"error":"missing"}`), nil
	})

	if _, err := NewController(nil).ImportOpenAPI(context.Background(), "http://localhost:8080/v3/api-docs"); err == nil {
		t.Fatal("expected error")
	}
}

func TestImportOpenAPIParsesSwaggerServers(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"swagger": "2.0",
			"host": "api.example.com",
			"basePath": "/v1",
			"schemes": ["https"],
			"paths": {"/health": {"get": {"summary": "Health"}}}
		}`), nil
	})

	got, err := NewController(nil).ImportOpenAPI(context.Background(), "http://localhost:8080/v2/api-docs")
	if err != nil {
		t.Fatalf("ImportOpenAPI returned error: %v", err)
	}
	if len(got.Servers) != 1 || got.Servers[0].URL != "https://api.example.com/v1" {
		t.Fatalf("servers = %+v", got.Servers)
	}
	if len(got.Endpoints) != 1 || got.Endpoints[0].ServerURL != "https://api.example.com/v1" {
		t.Fatalf("endpoints = %+v", got.Endpoints)
	}
}

func TestImportOpenAPIGeneratesBodySampleFromInlineSchema(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"openapi": "3.0.1",
			"paths": {
				"/users": {
					"post": {
						"requestBody": {
							"content": {
								"application/vnd.demo+json": {
									"schema": {
										"allOf": [
											{
												"type": "object",
												"properties": {
													"name": {"type": "string", "example": "Alice"}
												}
											},
											{
												"type": "object",
												"properties": {
													"id": {"type": "string", "format": "uuid"}
												}
											}
										]
									}
								}
							}
						}
					}
				}
			}
		}`), nil
	})

	got, err := NewController(nil).ImportOpenAPI(context.Background(), "http://localhost:8080/v3/api-docs")
	if err != nil {
		t.Fatalf("ImportOpenAPI returned error: %v", err)
	}
	if len(got.Endpoints) != 1 {
		t.Fatalf("got %d endpoints, want 1", len(got.Endpoints))
	}
	want := strings.Join([]string{
		"{",
		`  "id": "00000000-0000-0000-0000-000000000000",`,
		`  "name": "Alice"`,
		"}",
	}, "\n")
	if got.Endpoints[0].BodySample != want {
		t.Fatalf("BodySample = %q, want %q", got.Endpoints[0].BodySample, want)
	}
}

func TestImportOpenAPIGeneratesBodySampleFromSwaggerBodyParameter(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"swagger": "2.0",
			"paths": {
				"/login": {
					"post": {
						"parameters": [
							{
								"name": "body",
								"in": "body",
								"schema": {
									"type": "object",
									"properties": {
										"username": {"type": "string"},
										"password": {"type": "string"}
									}
								}
							}
						]
					}
				}
			}
		}`), nil
	})

	got, err := NewController(nil).ImportOpenAPI(context.Background(), "http://localhost:8080/v2/api-docs")
	if err != nil {
		t.Fatalf("ImportOpenAPI returned error: %v", err)
	}
	want := strings.Join([]string{
		"{",
		`  "password": "string",`,
		`  "username": "string"`,
		"}",
	}, "\n")
	if got.Endpoints[0].BodySample != want {
		t.Fatalf("BodySample = %q, want %q", got.Endpoints[0].BodySample, want)
	}
}

func TestImportOpenAPIHonorsContextCancellation(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return nil, req.Context().Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := NewController(nil).ImportOpenAPI(ctx, "http://localhost:8080/v3/api-docs"); err == nil {
		t.Fatal("expected error")
	}
}

func withOpenAPIHTTPClient(t *testing.T, fn roundTripFunc) {
	t.Helper()
	previous := openAPIHTTPClient
	openAPIHTTPClient = &http.Client{Transport: fn}
	t.Cleanup(func() {
		openAPIHTTPClient = previous
	})
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func assertEndpoint(t *testing.T, got OpenAPIEndpoint, want OpenAPIEndpoint) {
	t.Helper()
	if got.Method != want.Method ||
		got.Path != want.Path ||
		got.Summary != want.Summary ||
		got.OperationID != want.OperationID ||
		got.ServerURL != want.ServerURL ||
		got.Deprecated != want.Deprecated ||
		!sameAuth(got.Auth, want.Auth) ||
		got.BodySample != want.BodySample {
		t.Fatalf("endpoint = %+v, want %+v", got, want)
	}
	if strings.Join(got.Tags, ",") != strings.Join(want.Tags, ",") {
		t.Fatalf("endpoint tags = %+v, want %+v", got.Tags, want.Tags)
	}
	assertParameters(t, got.Parameters, want.Parameters)
}

func assertParameters(t *testing.T, got []OpenAPIParameter, want []OpenAPIParameter) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("parameters = %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index].Name != want[index].Name ||
			got[index].In != want[index].In ||
			got[index].Required != want[index].Required ||
			got[index].Sample != want[index].Sample {
			t.Fatalf("parameters = %+v, want %+v", got, want)
		}
	}
}

func sameAuth(got OpenAPIAuth, want OpenAPIAuth) bool {
	gotType := firstNonEmpty(got.Type, "none")
	wantType := firstNonEmpty(want.Type, "none")
	return gotType == wantType && got.Name == want.Name
}
