package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	openAPIImportTimeout = 5 * time.Second
	maxOpenAPIBodyBytes  = 5 << 20
)

var openAPIHTTPClient = http.DefaultClient

type OpenAPIImportResponse struct {
	SourceURL string            `json:"sourceUrl"`
	OpenAPI   string            `json:"openapi"`
	Title     string            `json:"title"`
	Version   string            `json:"version"`
	Servers   []OpenAPIServer   `json:"servers"`
	Endpoints []OpenAPIEndpoint `json:"endpoints"`
	RawJSON   json.RawMessage   `json:"rawJson"`
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

func (c *Controller) ImportOpenAPI(ctx context.Context, rawURL string) (OpenAPIImportResponse, error) {
	endpoint, err := validateOpenAPIURL(rawURL)
	if err != nil {
		return OpenAPIImportResponse{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, openAPIImportTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return OpenAPIImportResponse{}, err
	}
	req.Header.Set("Accept", "application/json, application/yaml;q=0.8, */*;q=0.1")

	resp, err := openAPIHTTPClient.Do(req)
	if err != nil {
		return OpenAPIImportResponse{}, fmt.Errorf("fetch OpenAPI document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return OpenAPIImportResponse{}, fmt.Errorf("fetch OpenAPI document: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenAPIBodyBytes+1))
	if err != nil {
		return OpenAPIImportResponse{}, fmt.Errorf("read OpenAPI document: %w", err)
	}
	if len(body) > maxOpenAPIBodyBytes {
		return OpenAPIImportResponse{}, fmt.Errorf("OpenAPI document is larger than %d bytes", maxOpenAPIBodyBytes)
	}

	return parseOpenAPIMetadata(endpoint, body)
}

func validateOpenAPIURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", errors.New("OpenAPI URL is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid OpenAPI URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("OpenAPI URL must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("OpenAPI URL requires a host")
	}
	if parsed.User != nil {
		return "", errors.New("OpenAPI URL must not include credentials")
	}
	return parsed.String(), nil
}

func parseOpenAPIMetadata(sourceURL string, body []byte) (OpenAPIImportResponse, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()

	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		return OpenAPIImportResponse{}, fmt.Errorf("OpenAPI document must be JSON: %w", err)
	}
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

	rawJSON := json.RawMessage(bytes.TrimSpace(body))
	return OpenAPIImportResponse{
		SourceURL: sourceURL,
		OpenAPI:   firstNonEmpty(openAPIVersion, swaggerVersion),
		Title:     title,
		Version:   version,
		Servers:   servers,
		Endpoints: endpoints,
		RawJSON:   rawJSON,
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

func endpointAuth(doc map[string]any, operation map[string]any) OpenAPIAuth {
	security, ok := operation["security"].([]any)
	if !ok {
		security, _ = doc["security"].([]any)
	}
	if len(security) == 0 {
		return OpenAPIAuth{Type: "none"}
	}
	schemes := securitySchemes(doc)
	for _, item := range security {
		requirement, ok := item.(map[string]any)
		if !ok {
			continue
		}
		names := make([]string, 0, len(requirement))
		for name := range requirement {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if auth := authFromSecurityScheme(schemes[name]); auth.Type != "" && auth.Type != "none" {
				return auth
			}
		}
	}
	return OpenAPIAuth{Type: "none"}
}

func securitySchemes(doc map[string]any) map[string]map[string]any {
	if components, ok := doc["components"].(map[string]any); ok {
		if schemes, ok := components["securitySchemes"].(map[string]any); ok {
			return mapValues(schemes)
		}
	}
	if definitions, ok := doc["securityDefinitions"].(map[string]any); ok {
		return mapValues(definitions)
	}
	return nil
}

func mapValues(values map[string]any) map[string]map[string]any {
	mapped := make(map[string]map[string]any, len(values))
	for key, value := range values {
		if object, ok := value.(map[string]any); ok {
			mapped[key] = object
		}
	}
	return mapped
}

func authFromSecurityScheme(scheme map[string]any) OpenAPIAuth {
	if scheme == nil {
		return OpenAPIAuth{Type: "none"}
	}
	schemeType, _ := scheme["type"].(string)
	switch strings.ToLower(schemeType) {
	case "http":
		httpScheme, _ := scheme["scheme"].(string)
		if strings.EqualFold(httpScheme, "bearer") {
			return OpenAPIAuth{Type: "bearer"}
		}
	case "apikey":
		name, _ := scheme["name"].(string)
		location, _ := scheme["in"].(string)
		switch location {
		case "header":
			return OpenAPIAuth{Type: "apiKey", Name: firstNonEmpty(name, "X-Api-Key")}
		case "cookie":
			return OpenAPIAuth{Type: "cookie", Name: firstNonEmpty(name, "session")}
		}
	}
	return OpenAPIAuth{Type: "none"}
}

func parseOpenAPIParameters(doc map[string]any, value any) []OpenAPIParameter {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	parameters := make([]OpenAPIParameter, 0, len(items))
	for _, item := range items {
		parameter, ok := resolveParameter(doc, item)
		if !ok {
			continue
		}
		name, _ := parameter["name"].(string)
		location, _ := parameter["in"].(string)
		if name == "" || location == "" {
			continue
		}
		required, _ := parameter["required"].(bool)
		description, _ := parameter["description"].(string)
		parameters = append(parameters, OpenAPIParameter{
			Name:        name,
			In:          location,
			Required:    required,
			Description: description,
			Sample:      parameterSample(doc, parameter),
		})
	}
	return parameters
}

func resolveParameter(doc map[string]any, value any) (map[string]any, bool) {
	parameter, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	ref, _ := parameter["$ref"].(string)
	if ref == "" {
		return parameter, true
	}
	resolved, ok := resolveJSONPointer(doc, ref)
	if !ok {
		return nil, false
	}
	resolvedParameter, ok := resolved.(map[string]any)
	return resolvedParameter, ok
}

func mergeOpenAPIParameters(pathParameters []OpenAPIParameter, operationParameters []OpenAPIParameter) []OpenAPIParameter {
	merged := make([]OpenAPIParameter, 0, len(pathParameters)+len(operationParameters))
	seen := map[string]int{}
	for _, parameter := range pathParameters {
		key := parameter.In + ":" + parameter.Name
		seen[key] = len(merged)
		merged = append(merged, parameter)
	}
	for _, parameter := range operationParameters {
		key := parameter.In + ":" + parameter.Name
		if index, ok := seen[key]; ok {
			merged[index] = parameter
			continue
		}
		seen[key] = len(merged)
		merged = append(merged, parameter)
	}
	return merged
}

func parameterSample(doc map[string]any, parameter map[string]any) string {
	if example, ok := parameter["example"]; ok {
		return fmt.Sprint(example)
	}
	if examples, ok := parameter["examples"].(map[string]any); ok {
		keys := make([]string, 0, len(examples))
		for key := range examples {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			example, ok := examples[key].(map[string]any)
			if !ok {
				continue
			}
			if value, ok := example["value"]; ok {
				return fmt.Sprint(value)
			}
		}
	}
	schema, _ := parameter["schema"].(map[string]any)
	if schema == nil {
		if _, ok := parameter["type"].(string); ok {
			schema = parameter
		} else {
			return "string"
		}
	}
	sample, ok := schemaSample(doc, schema, map[string]bool{})
	if !ok {
		return "1"
	}
	return sampleString(sample)
}

func sampleString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func requestBodySample(doc map[string]any, operation map[string]any) string {
	schema := requestBodyJSONSchema(operation)
	if schema == nil {
		schema = swaggerBodyParameterSchema(operation)
	}
	if schema == nil {
		return ""
	}

	sample, ok := schemaSample(doc, schema, map[string]bool{})
	if !ok {
		return ""
	}
	body, err := json.MarshalIndent(sample, "", "  ")
	if err != nil {
		return ""
	}
	return string(body)
}

func requestBodyJSONSchema(operation map[string]any) map[string]any {
	requestBody, ok := operation["requestBody"].(map[string]any)
	if !ok {
		return nil
	}
	if content, ok := requestBody["content"].(map[string]any); ok {
		if schema := mediaTypeSchema(content, "application/json"); schema != nil {
			return schema
		}
		for mediaType := range content {
			if strings.Contains(strings.ToLower(mediaType), "json") {
				if schema := mediaTypeSchema(content, mediaType); schema != nil {
					return schema
				}
			}
		}
	}
	return nil
}

func mediaTypeSchema(content map[string]any, mediaType string) map[string]any {
	media, ok := content[mediaType].(map[string]any)
	if !ok {
		return nil
	}
	schema, _ := media["schema"].(map[string]any)
	return schema
}

func swaggerBodyParameterSchema(operation map[string]any) map[string]any {
	parameters, ok := operation["parameters"].([]any)
	if !ok {
		return nil
	}
	for _, item := range parameters {
		parameter, ok := item.(map[string]any)
		if !ok || parameter["in"] != "body" {
			continue
		}
		schema, _ := parameter["schema"].(map[string]any)
		return schema
	}
	return nil
}

func schemaSample(doc map[string]any, schema map[string]any, seenRefs map[string]bool) (any, bool) {
	if ref, _ := schema["$ref"].(string); ref != "" {
		if seenRefs[ref] {
			return nil, false
		}
		resolved, ok := resolveSchemaRef(doc, ref)
		if !ok {
			return nil, false
		}
		seenRefs[ref] = true
		sample, ok := schemaSample(doc, resolved, seenRefs)
		delete(seenRefs, ref)
		return sample, ok
	}
	if allOf, ok := schema["allOf"].([]any); ok {
		return allOfSample(doc, allOf, seenRefs)
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		if variants, ok := schema[key].([]any); ok && len(variants) > 0 {
			if variant, ok := variants[0].(map[string]any); ok {
				return schemaSample(doc, variant, seenRefs)
			}
		}
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return enum[0], true
	}
	if example, ok := schema["example"]; ok {
		return example, true
	}
	if defaultValue, ok := schema["default"]; ok {
		return defaultValue, true
	}

	schemaType, _ := schema["type"].(string)
	if schemaType == "" {
		if _, ok := schema["properties"]; ok {
			schemaType = "object"
		} else if _, ok := schema["items"]; ok {
			schemaType = "array"
		}
	}
	switch schemaType {
	case "object":
		return objectSample(doc, schema, seenRefs), true
	case "array":
		return []any{}, true
	case "integer":
		return 0, true
	case "number":
		return 0, true
	case "boolean":
		return false, true
	case "string":
		return stringSample(schema), true
	default:
		return map[string]any{}, true
	}
}

func objectSample(doc map[string]any, schema map[string]any, seenRefs map[string]bool) map[string]any {
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	sample := make(map[string]any, len(keys))
	for _, key := range keys {
		propertySchema, ok := properties[key].(map[string]any)
		if !ok {
			continue
		}
		value, ok := schemaSample(doc, propertySchema, seenRefs)
		if ok {
			sample[key] = value
		}
	}
	return sample
}

func allOfSample(doc map[string]any, schemas []any, seenRefs map[string]bool) (any, bool) {
	merged := map[string]any{}
	for _, item := range schemas {
		schema, ok := item.(map[string]any)
		if !ok {
			continue
		}
		sample, ok := schemaSample(doc, schema, seenRefs)
		if !ok {
			continue
		}
		object, ok := sample.(map[string]any)
		if !ok {
			return sample, true
		}
		for key, value := range object {
			merged[key] = value
		}
	}
	return merged, true
}

func stringSample(schema map[string]any) string {
	format, _ := schema["format"].(string)
	switch format {
	case "date":
		return "2026-01-01"
	case "date-time":
		return "2026-01-01T00:00:00Z"
	case "email":
		return "user@example.com"
	case "uuid":
		return "00000000-0000-0000-0000-000000000000"
	default:
		return "string"
	}
}

func resolveSchemaRef(doc map[string]any, ref string) (map[string]any, bool) {
	resolved, ok := resolveJSONPointer(doc, ref)
	if !ok {
		return nil, false
	}
	schema, ok := resolved.(map[string]any)
	return schema, ok
}

func resolveJSONPointer(doc map[string]any, ref string) (any, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var current any = doc
	for _, token := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[decodeJSONPointerToken(token)]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func decodeJSONPointerToken(token string) string {
	token = strings.ReplaceAll(token, "~1", "/")
	token = strings.ReplaceAll(token, "~0", "~")
	return token
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
