package bridge

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestImportOpenAPIJSONAndYAMLParity(t *testing.T) {
	documents := map[string]string{
		"/openapi.json": `{
			"openapi": "3.1.0",
			"info": {"title": "Pet API", "version": "1.0.0"},
			"servers": [{"url": "https://api.example.com"}],
			"components": {
				"parameters": {
					"Limit": {
						"name": "limit",
						"in": "query",
						"schema": {"type": "integer", "default": 25}
					}
				},
				"schemas": {
					"CreatePet": {
						"type": "object",
						"properties": {"name": {"type": "string", "example": "Miso"}}
					}
				},
				"pathItems": {
					"Pets": {
						"parameters": [{"$ref": "#/components/parameters/Limit"}],
						"post": {
							"summary": "Create pet",
							"requestBody": {
								"content": {
									"application/json": {
										"schema": {"$ref": "#/components/schemas/CreatePet"}
									}
								}
							}
						}
					}
				}
			},
			"paths": {
				"/pets": {"$ref": "#/components/pathItems/Pets"}
			}
		}`,
		"/openapi.yaml": `
openapi: 3.1.0
info:
  title: Pet API
  version: 1.0.0
servers:
  - url: https://api.example.com
components:
  parameters:
    Limit:
      name: limit
      in: query
      schema:
        type: integer
        default: 25
  schemas:
    CreatePet:
      type: object
      properties:
        name:
          type: string
          example: Miso
  pathItems:
    Pets:
      parameters:
        - $ref: '#/components/parameters/Limit'
      post:
        summary: Create pet
        requestBody:
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/CreatePet'
paths:
  /pets:
    $ref: '#/components/pathItems/Pets'
`,
		"/swagger.json": `{
			"swagger": "2.0",
			"info": {"title": "Legacy Pet API", "version": "2.0.0"},
			"host": "legacy.example.com",
			"basePath": "/v2",
			"schemes": ["https"],
			"parameters": {
				"Trace": {"name": "trace", "in": "header", "type": "string"}
			},
			"definitions": {
				"Pet": {
					"type": "object",
					"properties": {"id": {"type": "integer"}}
				}
			},
			"paths": {
				"/pets": {
					"post": {
						"parameters": [
							{"$ref": "#/parameters/Trace"},
							{"name": "body", "in": "body", "schema": {"$ref": "#/definitions/Pet"}}
						]
					}
				}
			}
		}`,
		"/swagger.yaml": `
swagger: '2.0'
info:
  title: Legacy Pet API
  version: 2.0.0
host: legacy.example.com
basePath: /v2
schemes: [https]
parameters:
  Trace:
    name: trace
    in: header
    type: string
definitions:
  Pet:
    type: object
    properties:
      id:
        type: integer
paths:
  /pets:
    post:
      parameters:
        - $ref: '#/parameters/Trace'
        - name: body
          in: body
          schema:
            $ref: '#/definitions/Pet'
`,
	}
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		body, ok := documents[req.URL.Path]
		if !ok {
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
		return jsonResponse(http.StatusOK, body), nil
	})

	controller := NewController(nil)
	for _, pair := range [][2]string{
		{"/openapi.json", "/openapi.yaml"},
		{"/swagger.json", "/swagger.yaml"},
	} {
		jsonResult, err := controller.ImportOpenAPI(
			context.Background(),
			openAPIRequest("http://localhost:8080"+pair[0]),
		)
		if err != nil {
			t.Fatalf("import JSON %s: %v", pair[0], err)
		}
		yamlResult, err := controller.ImportOpenAPI(
			context.Background(),
			openAPIRequest("http://localhost:8080"+pair[1]),
		)
		if err != nil {
			t.Fatalf("import YAML %s: %v", pair[1], err)
		}
		jsonResult.SourceURL = ""
		yamlResult.SourceURL = ""
		if !reflect.DeepEqual(jsonResult, yamlResult) {
			t.Fatalf("JSON result = %+v, YAML result = %+v", jsonResult, yamlResult)
		}
	}
}

func TestImportOpenAPIResolvesOptInExternalReferences(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/openapi.yaml":
			return jsonResponse(http.StatusOK, `
openapi: 3.0.3
paths:
  /pets:
    post:
      requestBody:
        $ref: './shared.yaml#/requestBodies/CreatePet'
`), nil
		case "/shared.yaml":
			return jsonResponse(http.StatusOK, `
requestBodies:
  CreatePet:
    content:
      application/json:
        schema:
          type: object
          properties:
            name:
              type: string
              example: Miso
`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})

	request := openAPIRequest("http://localhost:8080/openapi.yaml")
	_, err := NewController(nil).ImportOpenAPI(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "external-reference consent") {
		t.Fatalf("error = %v, want external-reference consent error", err)
	}

	request.AllowExternalRefs = true
	got, err := NewController(nil).ImportOpenAPI(context.Background(), request)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned error: %v", err)
	}
	if len(got.Endpoints) != 1 || !strings.Contains(got.Endpoints[0].BodySample, `"name": "Miso"`) {
		t.Fatalf("endpoints = %+v", got.Endpoints)
	}
}

func TestImportOpenAPIRequiresPrivateNetworkConsent(t *testing.T) {
	called := false
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		called = true
		return jsonResponse(http.StatusOK, `{"openapi":"3.0.0","paths":{}}`), nil
	})

	request := OpenAPIImportRequest{URL: "http://127.0.0.1:8080/openapi.json"}
	_, err := NewController(nil).ImportOpenAPI(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "private-network consent") {
		t.Fatalf("error = %v, want private-network consent error", err)
	}
	if called {
		t.Fatal("private target was fetched before consent")
	}

	request.AllowPrivateNetwork = true
	if _, err := NewController(nil).ImportOpenAPI(context.Background(), request); err != nil {
		t.Fatalf("consented private import returned error: %v", err)
	}
}

func TestOpenAPINetworkPolicyRejectsPrivateAndReservedAddresses(t *testing.T) {
	tests := map[string]bool{
		"8.8.8.8":              false,
		"1.1.1.1":              false,
		"10.0.0.1":             true,
		"100.64.0.1":           true,
		"127.0.0.1":            true,
		"169.254.1.1":          true,
		"192.0.2.1":            true,
		"192.168.1.1":          true,
		"198.18.0.1":           true,
		"203.0.113.1":          true,
		"224.0.0.1":            true,
		"::1":                  true,
		"fc00::1":              true,
		"fe80::1":              true,
		"2001:db8::1":          true,
		"2001:4860:4860::8888": false,
	}
	for rawAddress, wantUnsafe := range tests {
		address := netip.MustParseAddr(rawAddress)
		if got := isUnsafeOpenAPIAddress(address); got != wantUnsafe {
			t.Errorf("isUnsafeOpenAPIAddress(%s) = %t, want %t", address, got, wantUnsafe)
		}
	}
}

func TestImportOpenAPIRedirectConsentAndDestinationSafety(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/start":
			return redirectResponse("http://127.0.0.1:8080/openapi.json"), nil
		case "/openapi.json":
			return jsonResponse(http.StatusOK, `{"openapi":"3.0.0","paths":{}}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})

	request := OpenAPIImportRequest{
		URL: "http://93.184.216.34/start",
	}
	_, err := NewController(nil).ImportOpenAPI(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "redirect consent") {
		t.Fatalf("error = %v, want redirect consent error", err)
	}

	request.AllowRedirects = true
	_, err = NewController(nil).ImportOpenAPI(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "private-network consent") {
		t.Fatalf("error = %v, want private redirect destination error", err)
	}

	request.AllowPrivateNetwork = true
	if _, err := NewController(nil).ImportOpenAPI(context.Background(), request); err != nil {
		t.Fatalf("consented redirect import returned error: %v", err)
	}
}

func TestImportOpenAPIRejectsRedirectChainsOverLimit(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		index, err := strconv.Atoi(strings.TrimPrefix(req.URL.Path, "/"))
		if err != nil {
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
		return redirectResponse(fmt.Sprintf("/%d", index+1)), nil
	})

	request := openAPIRequest("http://localhost:8080/0")
	request.AllowRedirects = true
	_, err := NewController(nil).ImportOpenAPI(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "redirect limit") {
		t.Fatalf("error = %v, want redirect limit error", err)
	}
}

func TestImportOpenAPIRejectsCyclicReferences(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"openapi": "3.1.0",
			"components": {
				"schemas": {
					"Node": {"$ref": "#/components/schemas/Node"}
				}
			},
			"paths": {}
		}`), nil
	})

	_, err := NewController(nil).ImportOpenAPI(
		context.Background(),
		openAPIRequest("http://localhost:8080/openapi.json"),
	)
	if err == nil || !strings.Contains(err.Error(), "cyclic OpenAPI reference") {
		t.Fatalf("error = %v, want cyclic reference error", err)
	}
}

func TestImportOpenAPIRejectsOversizedDocuments(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, strings.Repeat(" ", maxOpenAPIBodyBytes+1)), nil
	})

	_, err := NewController(nil).ImportOpenAPI(
		context.Background(),
		openAPIRequest("http://localhost:8080/openapi.json"),
	)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("error = %v, want document size error", err)
	}
}

func TestImportOpenAPIRejectsExternalDocumentChainsOverLimit(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		index, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/"), ".json"))
		if err != nil {
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
		if index == 0 {
			return jsonResponse(http.StatusOK, `{
				"openapi": "3.0.0",
				"paths": {},
				"x-chain": {"$ref": "1.json"}
			}`), nil
		}
		return jsonResponse(
			http.StatusOK,
			fmt.Sprintf(`{"x-chain":{"$ref":"%d.json"}}`, index+1),
		), nil
	})

	request := openAPIRequest("http://localhost:8080/0.json")
	request.AllowExternalRefs = true
	_, err := NewController(nil).ImportOpenAPI(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "document limit") {
		t.Fatalf("error = %v, want document limit error", err)
	}
}

func TestImportOpenAPIRejectsMultipleYAMLDocuments(t *testing.T) {
	withOpenAPIHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `
openapi: 3.0.0
paths: {}
---
openapi: 3.0.1
paths: {}
`), nil
	})

	_, err := NewController(nil).ImportOpenAPI(
		context.Background(),
		openAPIRequest("http://localhost:8080/openapi.yaml"),
	)
	if err == nil || !strings.Contains(err.Error(), "exactly one YAML document") {
		t.Fatalf("error = %v, want single-document error", err)
	}
}

func redirectResponse(location string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{location}},
		Body:       http.NoBody,
	}
}
