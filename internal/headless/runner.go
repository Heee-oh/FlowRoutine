package headless

import (
	"context"
	"errors"
	"sync"
	"time"

	"flowroutine/internal/bridge"
)

type ValidationError struct {
	Err error
}

func (err *ValidationError) Error() string {
	return "validation failed: " + err.Err.Error()
}

func (err *ValidationError) Unwrap() error {
	return err.Err
}

type ExecutionError struct {
	Err error
}

func (err *ExecutionError) Error() string {
	return "execution failed: " + err.Err.Error()
}

func (err *ExecutionError) Unwrap() error {
	return err.Err
}

func Run(ctx context.Context, file *ScenarioFile, runtimeBindings map[string]string) (*RunReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, &ExecutionError{Err: err}
	}
	if err := file.ValidateDefinition(); err != nil {
		return nil, &ValidationError{Err: err}
	}
	if err := file.Scenario.ValidateRuntimeBindings(runtimeBindings); err != nil {
		return nil, &ValidationError{Err: err}
	}

	emitter := newMetricsEmitter()
	controller := bridge.NewController(emitter)
	request := bridge.StartRequest{
		Config:          file.Scenario.Config,
		BatchIntervalMS: file.Scenario.BatchIntervalMS,
		RuntimeBindings: runtimeBindings,
	}
	if _, err := controller.Preflight(request); err != nil {
		return nil, &ValidationError{Err: err}
	}
	response, err := controller.Start(ctx, request)
	if err != nil {
		return nil, &ExecutionError{Err: err}
	}

	select {
	case final := <-emitter.final:
		return BuildRunReport(file, response.Preflight, final, emitter.BatchCount(), time.Now()), nil
	case <-ctx.Done():
		stopErr := controller.Stop()
		if stopErr != nil {
			return nil, &ExecutionError{Err: errors.Join(ctx.Err(), stopErr)}
		}
		return nil, &ExecutionError{Err: ctx.Err()}
	}
}

type metricsEmitter struct {
	mu         sync.Mutex
	final      chan bridge.MetricsBatch
	batchCount int
	finalSent  bool
}

func newMetricsEmitter() *metricsEmitter {
	return &metricsEmitter{final: make(chan bridge.MetricsBatch, 1)}
}

func (emitter *metricsEmitter) Emit(_ context.Context, eventName string, data any) {
	if eventName != bridge.MetricsBatchEvent {
		return
	}
	batch, ok := data.(bridge.MetricsBatch)
	if !ok {
		pointer, pointerOK := data.(*bridge.MetricsBatch)
		if !pointerOK || pointer == nil {
			return
		}
		batch = *pointer
	}

	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	emitter.batchCount++
	if batch.Running || emitter.finalSent {
		return
	}
	emitter.finalSent = true
	emitter.final <- batch
}

func (emitter *metricsEmitter) BatchCount() int {
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	return emitter.batchCount
}

func IsValidationError(err error) bool {
	var validation *ValidationError
	return errors.As(err, &validation)
}

func IsExecutionError(err error) bool {
	var execution *ExecutionError
	return errors.As(err, &execution)
}
