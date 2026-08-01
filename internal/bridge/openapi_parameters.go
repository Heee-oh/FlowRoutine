package bridge

import (
	"encoding/json"
	"fmt"
	"sort"
)

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
