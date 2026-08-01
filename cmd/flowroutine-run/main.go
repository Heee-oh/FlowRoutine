package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"flowroutine/internal/headless"
)

const (
	exitSuccess    = 0
	exitValidation = 2
	exitExecution  = 3
	exitSLO        = 4
)

func main() {
	os.Exit(mainExitCode())
}

func mainExitCode() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runCLI(ctx, os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv)
}

func runCLI(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	flags := flag.NewFlagSet("flowroutine-run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var envBindings stringListFlag
	var fileBindings stringListFlag
	jsonReport := flags.String("json-report", "-", "JSON report path; '-' writes to stdout")
	junitReport := flags.String("junit-report", "", "optional JUnit XML report path")
	flags.Var(&envBindings, "bind-env", "bind SECRET_NAME to an environment variable: SECRET_NAME=ENV_NAME (repeatable)")
	flags.Var(&fileBindings, "bind-file", "bind SECRET_NAME to a file: SECRET_NAME=PATH (repeatable)")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage: flowroutine-run [options] SCENARIO.json")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitSuccess
		}
		return exitValidation
	}
	if flags.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "validation failed: exactly one scenario file is required")
		flags.Usage()
		return exitValidation
	}
	if *jsonReport == "-" && *junitReport == "-" {
		_, _ = fmt.Fprintln(stderr, "validation failed: JSON and JUnit reports cannot both use stdout")
		return exitValidation
	}

	scenarioPath := flags.Arg(0)
	if sameOutputPath(*jsonReport, *junitReport) {
		_, _ = fmt.Fprintln(stderr, "validation failed: JSON and JUnit reports must use different paths")
		return exitValidation
	}
	if sameOutputPath(scenarioPath, *jsonReport) || sameOutputPath(scenarioPath, *junitReport) {
		_, _ = fmt.Fprintln(stderr, "validation failed: a report path must not overwrite the scenario file")
		return exitValidation
	}
	file, err := headless.LoadScenario(scenarioPath)
	if err != nil {
		return handleRunError(stdout, stderr, *junitReport, filepath.Base(scenarioPath), "", "validation", err, exitValidation)
	}
	bindings, err := resolveBindings(envBindings, fileBindings, lookupEnv)
	if err != nil {
		return handleRunError(stdout, stderr, *junitReport, file.Scenario.Name, file.Scenario.ID, "validation", err, exitValidation)
	}

	report, err := headless.Run(ctx, file, bindings)
	if err != nil {
		code := exitExecution
		kind := "execution"
		if headless.IsValidationError(err) {
			code = exitValidation
			kind = "validation"
		}
		if cause := errors.Unwrap(err); cause != nil {
			err = cause
		}
		return handleRunError(stdout, stderr, *junitReport, file.Scenario.Name, file.Scenario.ID, kind, err, code)
	}

	jsonData, err := headless.MarshalJSONReport(report)
	if err != nil {
		return handleRunError(stdout, stderr, *junitReport, file.Scenario.Name, file.Scenario.ID, "execution", err, exitExecution)
	}
	if err := writeReport(*jsonReport, jsonData, stdout); err != nil {
		return handleRunError(stdout, stderr, *junitReport, file.Scenario.Name, file.Scenario.ID, "execution", fmt.Errorf("write JSON report: %w", err), exitExecution)
	}
	if *junitReport != "" {
		junitData, err := headless.MarshalJUnitReport(report)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "execution failed: %v\n", err)
			return exitExecution
		}
		if err := writeReport(*junitReport, junitData, stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "execution failed: write JUnit report: %v\n", err)
			return exitExecution
		}
	}
	if report.QualityGate.Status == headless.QualityGateFail {
		_, _ = fmt.Fprintln(stderr, "SLO failed: one or more quality-gate checks failed")
		return exitSLO
	}
	return exitSuccess
}

func resolveBindings(envBindings []string, fileBindings []string, lookupEnv func(string) (string, bool)) (map[string]string, error) {
	bindings := make(map[string]string, len(envBindings)+len(fileBindings))
	for _, raw := range envBindings {
		name, environmentName, err := parseBinding(raw)
		if err != nil {
			return nil, fmt.Errorf("bind-env: %w", err)
		}
		if err := rejectDuplicateBinding(bindings, name); err != nil {
			return nil, err
		}
		value, exists := lookupEnv(environmentName)
		if !exists || value == "" {
			return nil, fmt.Errorf("environment variable %q for runtime binding %q is missing or empty", environmentName, name)
		}
		bindings[name] = value
	}
	for _, raw := range fileBindings {
		name, path, err := parseBinding(raw)
		if err != nil {
			return nil, fmt.Errorf("bind-file: %w", err)
		}
		if err := rejectDuplicateBinding(bindings, name); err != nil {
			return nil, err
		}
		value, err := readBindingFile(path)
		if err != nil {
			return nil, fmt.Errorf("read runtime binding %q: %w", name, err)
		}
		bindings[name] = value
	}
	return bindings, nil
}

func parseBinding(raw string) (string, string, error) {
	name, source, exists := strings.Cut(raw, "=")
	name = strings.TrimSpace(name)
	source = strings.TrimSpace(source)
	if !exists || name == "" || source == "" {
		return "", "", errors.New("binding must use SECRET_NAME=SOURCE")
	}
	if !headless.ValidRuntimeBindingName(name) {
		return "", "", fmt.Errorf("binding name %q must match SECRET_[A-Z0-9_]+", name)
	}
	return name, source, nil
}

func rejectDuplicateBinding(bindings map[string]string, name string) error {
	if _, exists := bindings[name]; exists {
		return fmt.Errorf("runtime binding %q was provided more than once", name)
	}
	return nil
}

func readBindingFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("binding source must be a regular file")
	}
	limited := &io.LimitedReader{R: file, N: 64<<10 + 1}
	value, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if len(value) > 64<<10 {
		return "", errors.New("binding file exceeds the 65536-byte limit")
	}
	value = bytes.TrimSuffix(value, []byte("\n"))
	value = bytes.TrimSuffix(value, []byte("\r"))
	if len(value) == 0 {
		return "", errors.New("binding file is empty")
	}
	return string(value), nil
}

func writeReport(path string, data []byte, stdout io.Writer) error {
	if path == "" {
		return nil
	}
	if path == "-" {
		_, err := stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func sameOutputPath(left string, right string) bool {
	if left == "" || right == "" || left == "-" || right == "-" {
		return false
	}
	leftPath, leftErr := filepath.Abs(left)
	rightPath, rightErr := filepath.Abs(right)
	if leftErr == nil && rightErr == nil && filepath.Clean(leftPath) == filepath.Clean(rightPath) {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func handleRunError(stdout io.Writer, stderr io.Writer, junitPath string, scenarioName string, scenarioID string, kind string, runError error, exitCode int) int {
	_, _ = fmt.Fprintf(stderr, "%s failed: %v\n", kind, runError)
	if junitPath == "" {
		return exitCode
	}
	data, err := headless.MarshalJUnitError(scenarioName, scenarioID, kind, runError)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "execution failed: %v\n", err)
		return exitExecution
	}
	if err := writeReport(junitPath, data, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "execution failed: write JUnit report: %v\n", err)
		return exitExecution
	}
	return exitCode
}

type stringListFlag []string

func (values *stringListFlag) String() string {
	return strings.Join(*values, ",")
}

func (values *stringListFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}
