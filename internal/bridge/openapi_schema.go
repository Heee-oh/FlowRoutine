package bridge

import (
	"encoding/json"
	"sort"
	"strings"
)

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
