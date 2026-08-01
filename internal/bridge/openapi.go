package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type OpenAPIImportRequest struct {
	URL                 string `json:"url"`
	AllowPrivateNetwork bool   `json:"allowPrivateNetwork"`
	AllowRedirects      bool   `json:"allowRedirects"`
	AllowExternalRefs   bool   `json:"allowExternalRefs"`
}

type OpenAPIImportResponse struct {
	SourceURL string            `json:"sourceUrl"`
	OpenAPI   string            `json:"openapi"`
	Title     string            `json:"title"`
	Version   string            `json:"version"`
	Servers   []OpenAPIServer   `json:"servers"`
	Endpoints []OpenAPIEndpoint `json:"endpoints"`
}

type OpenAPIServer struct {
	URL         string `json:"url"`
	Description string `json:"description"`
}

type OpenAPIEndpoint struct {
	Method      string             `json:"method"`
	Path        string             `json:"path"`
	Summary     string             `json:"summary"`
	OperationID string             `json:"operationId"`
	Tags        []string           `json:"tags"`
	ServerURL   string             `json:"serverUrl"`
	Deprecated  bool               `json:"deprecated"`
	Auth        OpenAPIAuth        `json:"auth"`
	Parameters  []OpenAPIParameter `json:"parameters"`
	BodySample  string             `json:"bodySample"`
}

type OpenAPIAuth struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type OpenAPIParameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
	Sample      string `json:"sample"`
}

func (c *Controller) ImportOpenAPI(ctx context.Context, request OpenAPIImportRequest) (OpenAPIImportResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, openAPIImportTimeout)
	defer cancel()

	sourceURL, doc, err := loadOpenAPIDocument(ctx, request)
	if err != nil {
		return OpenAPIImportResponse{}, err
	}
	return parseOpenAPIMetadata(sourceURL, doc)
}

func parseOpenAPIMetadata(sourceURL string, doc map[string]any) (OpenAPIImportResponse, error) {
	if len(doc) == 0 {
		return OpenAPIImportResponse{}, errors.New("OpenAPI document is empty")
	}

	openAPIVersion, _ := doc["openapi"].(string)
	swaggerVersion, _ := doc["swagger"].(string)
	if openAPIVersion == "" && swaggerVersion == "" {
		return OpenAPIImportResponse{}, errors.New("document is missing openapi or swagger version")
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return OpenAPIImportResponse{}, errors.New("OpenAPI document is missing paths")
	}

	var title string
	var version string
	if info, ok := doc["info"].(map[string]any); ok {
		title, _ = info["title"].(string)
		version, _ = info["version"].(string)
	}
	servers := parseOpenAPIServers(doc, sourceURL)
	endpoints := parseOpenAPIEndpoints(doc, paths, servers)

	return OpenAPIImportResponse{
		SourceURL: sourceURL,
		OpenAPI:   firstNonEmpty(openAPIVersion, swaggerVersion),
		Title:     title,
		Version:   version,
		Servers:   servers,
		Endpoints: endpoints,
	}, nil
}

func parseOpenAPIServers(doc map[string]any, sourceURL string) []OpenAPIServer {
	if servers, ok := doc["servers"].([]any); ok {
		parsed := make([]OpenAPIServer, 0, len(servers))
		for _, item := range servers {
			server, ok := item.(map[string]any)
			if !ok {
				continue
			}
			serverURL, _ := server["url"].(string)
			serverURL = strings.TrimSpace(serverURL)
			if serverURL == "" {
				continue
			}
			description, _ := server["description"].(string)
			parsed = append(parsed, OpenAPIServer{
				URL:         serverURL,
				Description: description,
			})
		}
		if len(parsed) > 0 {
			return parsed
		}
	}

	if swagger, _ := doc["swagger"].(string); swagger != "" {
		host, _ := doc["host"].(string)
		basePath, _ := doc["basePath"].(string)
		if host != "" {
			scheme := "http"
			if schemes, ok := doc["schemes"].([]any); ok && len(schemes) > 0 {
				if parsedScheme, ok := schemes[0].(string); ok && parsedScheme != "" {
					scheme = parsedScheme
				}
			}
			return []OpenAPIServer{{URL: fmt.Sprintf("%s://%s%s", scheme, host, basePath)}}
		}
	}

	parsed, err := url.Parse(sourceURL)
	if err != nil {
		return nil
	}
	return []OpenAPIServer{{URL: fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)}}
}

func parseOpenAPIEndpoints(doc map[string]any, paths map[string]any, servers []OpenAPIServer) []OpenAPIEndpoint {
	endpoints := make([]OpenAPIEndpoint, 0)
	defaultServerURL := ""
	if len(servers) > 0 {
		defaultServerURL = servers[0].URL
	}

	for path, rawPathItem := range paths {
		pathItem, ok := rawPathItem.(map[string]any)
		if !ok {
			continue
		}
		pathParameters := parseOpenAPIParameters(doc, pathItem["parameters"])
		for _, method := range openAPIMethods {
			rawOperation, ok := pathItem[method]
			if !ok {
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				continue
			}
			summary, _ := operation["summary"].(string)
			if summary == "" {
				summary, _ = operation["description"].(string)
			}
			operationID, _ := operation["operationId"].(string)
			deprecated, _ := operation["deprecated"].(bool)
			parameters := mergeOpenAPIParameters(pathParameters, parseOpenAPIParameters(doc, operation["parameters"]))
			endpoints = append(endpoints, OpenAPIEndpoint{
				Method:      strings.ToUpper(method),
				Path:        path,
				Summary:     summary,
				OperationID: operationID,
				Tags:        stringSlice(operation["tags"]),
				ServerURL:   defaultServerURL,
				Deprecated:  deprecated,
				Auth:        endpointAuth(doc, operation),
				Parameters:  parameters,
				BodySample:  requestBodySample(doc, operation),
			})
		}
	}

	sort.SliceStable(endpoints, func(i, j int) bool {
		if endpoints[i].Path != endpoints[j].Path {
			return endpoints[i].Path < endpoints[j].Path
		}
		return methodSortIndex(endpoints[i].Method) < methodSortIndex(endpoints[j].Method)
	})
	return endpoints
}

var openAPIMethods = []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}

func methodSortIndex(method string) int {
	lower := strings.ToLower(method)
	for index, candidate := range openAPIMethods {
		if candidate == lower {
			return index
		}
	}
	return len(openAPIMethods)
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	parsed := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if ok && text != "" {
			parsed = append(parsed, text)
		}
	}
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
