package headless

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"flowroutine/internal/bridge"
	"flowroutine/internal/engine"
)

const (
	ScenarioSchemaVersion = 1
	MaxScenarioFileBytes  = 5 << 20
	maxScenarioIDBytes    = 128
	maxScenarioNameBytes  = 256
)

var (
	scenarioIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	runtimeBindingPattern   = regexp.MustCompile(`^SECRET_[A-Z0-9_]+$`)
	templatePattern         = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*\}\}`)
	pureSecretPattern       = regexp.MustCompile(`^\s*\{\{\s*(SECRET_[A-Z0-9_]+)\s*\}\}\s*$`)
	headerSecretPattern     = regexp.MustCompile(`(?i)^\s*(?:(?:aws4-hmac-sha256|basic|bearer|digest|hawk|token)\s+)?\{\{\s*(SECRET_[A-Z0-9_]+)\s*\}\}\s*$`)
	sensitiveHeaderPattern  = regexp.MustCompile(`(?i)^\s*(?:aws4-hmac-sha256|basic|bearer|digest|hawk|token)\s+`)
	sensitiveHeaderNames    = stringSet("auth", "authentication", "authorization", "proxyauthorization", "cookie", "setcookie", "apikey", "xauth", "xapikey", "xauthkey", "xauthorization")
	sensitiveParameterNames = stringSet(
		"accesskey", "accesskeyid", "accesstoken", "apikey", "auth", "authorization",
		"awsaccesskeyid", "clientsecret", "code", "cookie", "googleaccessid", "idtoken",
		"keypairid", "passphrase", "password", "passwd", "privatekey", "refreshtoken",
		"sas", "secretkey", "sessiontoken", "sharedaccesssignature", "sig", "session",
		"sessionid", "setcookie",
	)
)

type ScenarioFile struct {
	SchemaVersion int       `json:"schemaVersion"`
	Scenario      *Scenario `json:"scenario"`
}

type Scenario struct {
	ID                      string             `json:"id"`
	Name                    string             `json:"name"`
	Revision                int                `json:"revision"`
	Config                  bridge.LoadConfig  `json:"config"`
	BatchIntervalMS         int                `json:"batchIntervalMs"`
	QualityGate             *QualityGateConfig `json:"qualityGate,omitempty"`
	RequiredRuntimeBindings []string           `json:"requiredRuntimeBindings"`
}

func LoadScenario(path string) (*ScenarioFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open scenario: %w", err)
	}
	defer file.Close()

	scenario, err := DecodeScenario(file)
	if err != nil {
		return nil, fmt.Errorf("load scenario %q: %w", path, err)
	}
	return scenario, nil
}

func DecodeScenario(reader io.Reader) (*ScenarioFile, error) {
	if reader == nil {
		return nil, errors.New("scenario reader is required")
	}
	limited := &io.LimitedReader{R: reader, N: MaxScenarioFileBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read scenario: %w", err)
	}
	if len(data) > MaxScenarioFileBytes {
		return nil, fmt.Errorf("scenario exceeds the %d-byte limit", MaxScenarioFileBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file ScenarioFile
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("decode scenario: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decode scenario: multiple JSON values are not allowed")
		}
		return nil, fmt.Errorf("decode scenario: %w", err)
	}
	if err := file.ValidateDefinition(); err != nil {
		return nil, err
	}
	return &file, nil
}

func (file *ScenarioFile) ValidateDefinition() error {
	if file == nil {
		return errors.New("scenario file is required")
	}
	if file.SchemaVersion != ScenarioSchemaVersion {
		return fmt.Errorf("schemaVersion must be %d", ScenarioSchemaVersion)
	}
	if file.Scenario == nil {
		return errors.New("scenario is required")
	}
	return file.Scenario.ValidateDefinition()
}

func (scenario *Scenario) ValidateDefinition() error {
	if scenario == nil {
		return errors.New("scenario is required")
	}
	scenario.ID = strings.TrimSpace(scenario.ID)
	scenario.Name = strings.TrimSpace(scenario.Name)
	if err := validateScenarioText("scenario id", scenario.ID, maxScenarioIDBytes); err != nil {
		return err
	}
	if !scenarioIDPattern.MatchString(scenario.ID) {
		return errors.New("scenario id must contain only letters, numbers, '.', '_', or '-'")
	}
	if err := validateScenarioText("scenario name", scenario.Name, maxScenarioNameBytes); err != nil {
		return err
	}
	if scenario.Revision < 1 {
		return errors.New("scenario revision must be at least 1")
	}

	declared := make(map[string]struct{}, len(scenario.RequiredRuntimeBindings))
	for index, rawName := range scenario.RequiredRuntimeBindings {
		name := strings.TrimSpace(rawName)
		if !ValidRuntimeBindingName(name) {
			return fmt.Errorf("requiredRuntimeBindings[%d] must match SECRET_[A-Z0-9_]+", index)
		}
		if _, exists := declared[name]; exists {
			return fmt.Errorf("runtime binding %q is declared more than once", name)
		}
		declared[name] = struct{}{}
		scenario.RequiredRuntimeBindings[index] = name
	}
	if len(declared) > engine.MaxRuntimeVariables {
		return fmt.Errorf("at most %d runtime bindings are allowed", engine.MaxRuntimeVariables)
	}

	used, err := validateSecretStorage(scenario.Config)
	if err != nil {
		return err
	}
	for name := range used {
		if _, exists := declared[name]; !exists {
			return fmt.Errorf("runtime binding %q is used but not declared", name)
		}
	}
	for name := range declared {
		if _, exists := used[name]; !exists {
			return fmt.Errorf("runtime binding %q is declared but not used", name)
		}
	}
	return nil
}

func (scenario *Scenario) ValidateRuntimeBindings(bindings map[string]string) error {
	if err := scenario.ValidateDefinition(); err != nil {
		return err
	}
	required := make(map[string]struct{}, len(scenario.RequiredRuntimeBindings))
	for _, name := range scenario.RequiredRuntimeBindings {
		required[name] = struct{}{}
		value, exists := bindings[name]
		if !exists || value == "" {
			return fmt.Errorf("runtime binding %q is required", name)
		}
		if len(value) > engine.MaxRuntimeVariableBytes {
			return fmt.Errorf("runtime binding %q exceeds the %d-byte limit", name, engine.MaxRuntimeVariableBytes)
		}
		if !utf8.ValidString(value) {
			return fmt.Errorf("runtime binding %q must be valid UTF-8", name)
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("runtime binding %q must not contain NUL, CR, or LF", name)
		}
	}
	for name := range bindings {
		if _, exists := required[name]; !exists {
			return fmt.Errorf("runtime binding %q was provided but is not required", name)
		}
	}
	return nil
}

func ValidRuntimeBindingName(name string) bool {
	return runtimeBindingPattern.MatchString(name)
}

func validateScenarioText(label string, value string, limit int) error {
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", label)
	}
	if len(value) > limit {
		return fmt.Errorf("%s must be at most %d bytes", label, limit)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s must not contain control characters", label)
		}
	}
	return nil
}

func validateSecretStorage(config bridge.LoadConfig) (map[string]struct{}, error) {
	used := make(map[string]struct{})
	values := []string{config.URL, config.Body}
	if err := validateURLSecrets("config url", config.URL); err != nil {
		return nil, err
	}
	if err := validateHeaderSecrets("config headers", config.Headers); err != nil {
		return nil, err
	}
	if err := validateBodySecrets("config body", config.Body); err != nil {
		return nil, err
	}
	for _, header := range config.Headers {
		values = append(values, header.Value)
	}
	for index, step := range config.ScenarioSteps {
		label := fmt.Sprintf("scenario step %d", index+1)
		if step.Kind == "" || step.Kind == string(engine.StepRequest) {
			if err := validateURLSecrets(label+" url", step.URL); err != nil {
				return nil, err
			}
			if err := validateHeaderSecrets(label+" headers", step.Headers); err != nil {
				return nil, err
			}
			if err := validateBodySecrets(label+" body", step.Body); err != nil {
				return nil, err
			}
			values = append(values, step.URL, step.Body)
			for _, header := range step.Headers {
				values = append(values, header.Value)
			}
		}
	}

	for _, value := range values {
		for _, match := range templatePattern.FindAllStringSubmatch(value, -1) {
			name := match[1]
			if !strings.HasPrefix(strings.ToUpper(name), "SECRET_") {
				continue
			}
			if !ValidRuntimeBindingName(name) {
				return nil, fmt.Errorf("runtime secret template %q must match SECRET_[A-Z0-9_]+", name)
			}
			used[name] = struct{}{}
		}
	}
	return used, nil
}

func validateHeaderSecrets(label string, headers []bridge.Header) error {
	for _, header := range headers {
		if !isSensitiveHeaderName(header.Name) && !sensitiveHeaderPattern.MatchString(header.Value) {
			continue
		}
		if !headerSecretPattern.MatchString(header.Value) {
			return fmt.Errorf("%s header %q contains a sensitive literal; use a declared {{SECRET_*}} binding", label, header.Name)
		}
	}
	return nil
}

func validateURLSecrets(label string, rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil // The shared bridge preflight returns the canonical URL diagnostic.
	}
	if target.User != nil {
		if username := target.User.Username(); username != "" && !pureSecretPattern.MatchString(username) {
			return fmt.Errorf("%s contains a literal username; use a declared {{SECRET_*}} binding", label)
		}
		if password, set := target.User.Password(); set && !pureSecretPattern.MatchString(password) {
			return fmt.Errorf("%s contains a literal password; use a declared {{SECRET_*}} binding", label)
		}
	}
	if err := validateRawParameters(label+" query", target.RawQuery); err != nil {
		return err
	}
	fragment := target.Fragment
	if queryAt := strings.IndexByte(fragment, '?'); queryAt >= 0 {
		fragment = fragment[queryAt+1:]
	}
	if strings.Contains(fragment, "=") {
		if err := validateRawParameters(label+" fragment", fragment); err != nil {
			return err
		}
	}
	return nil
}

func validateBodySecrets(label string, body string) error {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil
	}
	var document any
	if json.Unmarshal([]byte(trimmed), &document) == nil {
		return validateJSONSecrets(label, document)
	}
	if strings.Contains(trimmed, "=") {
		return validateRawParameters(label, trimmed)
	}
	return nil
}

func validateJSONSecrets(label string, value any) error {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if err := validateJSONSecrets(label, item); err != nil {
				return err
			}
		}
	case map[string]any:
		for name, item := range typed {
			if isSensitiveParameterName(name) {
				text, ok := item.(string)
				if !ok || !pureSecretPattern.MatchString(text) {
					return fmt.Errorf("%s field %q contains a sensitive literal; use a declared {{SECRET_*}} binding", label, name)
				}
				continue
			}
			if err := validateJSONSecrets(label, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRawParameters(label string, raw string) error {
	for _, part := range strings.Split(raw, "&") {
		encodedName, encodedValue, hasValue := strings.Cut(part, "=")
		name, err := url.QueryUnescape(encodedName)
		if err != nil || !isSensitiveParameterName(name) {
			continue
		}
		if !hasValue {
			return fmt.Errorf("%s parameter %q contains a sensitive literal; use a declared {{SECRET_*}} binding", label, name)
		}
		value, err := url.QueryUnescape(encodedValue)
		if err != nil || !pureSecretPattern.MatchString(value) {
			return fmt.Errorf("%s parameter %q contains a sensitive literal; use a declared {{SECRET_*}} binding", label, name)
		}
	}
	return nil
}

func isSensitiveHeaderName(name string) bool {
	normalized := normalizeSensitiveName(name)
	return sensitiveHeaderNames[normalized] ||
		strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "signature") ||
		strings.Contains(normalized, "credential") ||
		strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "accesskey") ||
		strings.Contains(normalized, "privatekey") ||
		strings.Contains(normalized, "password") ||
		strings.HasSuffix(normalized, "authorization")
}

func isSensitiveParameterName(name string) bool {
	normalized := normalizeSensitiveName(name)
	return sensitiveParameterNames[normalized] ||
		strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "signature") ||
		strings.Contains(normalized, "credential") ||
		strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "accesskey")
}

func normalizeSensitiveName(name string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(name) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func stringSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
