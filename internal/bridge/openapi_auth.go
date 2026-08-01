package bridge

import (
	"sort"
	"strings"
)

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
