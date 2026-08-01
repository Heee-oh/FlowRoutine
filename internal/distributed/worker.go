package distributed

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"flowroutine/internal/engine"
)

const (
	MinScheduledStartLead = 100 * time.Millisecond
	MaxScheduledStartLead = 5 * time.Minute
)

type WorkerServer struct {
	id  string
	now func() time.Time

	mu             sync.Mutex
	generation     uint64
	runID          string
	planID         string
	planRevision   uint64
	planDigest     string
	state          WorkerState
	loadEngine     *engine.Engine
	scheduleCancel context.CancelFunc
	scheduledAt    time.Time
	startedAt      time.Time
	stoppedAt      time.Time
	lastError      string
}

func NewWorkerServer(id string) (*WorkerServer, error) {
	if err := validateIdentifier("worker id", id); err != nil {
		return nil, err
	}
	return &WorkerServer{id: id, now: time.Now, state: WorkerIdle}, nil
}

func (worker *WorkerServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.TLS == nil || len(request.TLS.VerifiedChains) == 0 {
		writeError(response, http.StatusUnauthorized, "verified mutual TLS is required")
		return
	}
	switch request.URL.Path {
	case statusPath:
		if request.Method != http.MethodGet {
			writeMethodNotAllowed(response, http.MethodGet)
			return
		}
		worker.handleStatus(response, request)
	case preparePath:
		if request.Method != http.MethodPost {
			writeMethodNotAllowed(response, http.MethodPost)
			return
		}
		worker.handlePrepare(response, request)
	case startPath:
		if request.Method != http.MethodPost {
			writeMethodNotAllowed(response, http.MethodPost)
			return
		}
		worker.handleStart(response, request)
	case snapshotPath:
		if request.Method != http.MethodGet {
			writeMethodNotAllowed(response, http.MethodGet)
			return
		}
		worker.handleSnapshot(response, request)
	case stopPath:
		if request.Method != http.MethodPost {
			writeMethodNotAllowed(response, http.MethodPost)
			return
		}
		worker.handleStop(response, request)
	default:
		writeError(response, http.StatusNotFound, "endpoint not found")
	}
}

func (worker *WorkerServer) handleStatus(response http.ResponseWriter, request *http.Request) {
	worker.mu.Lock()
	if runID := request.URL.Query().Get("runId"); runID != "" && runID != worker.runID {
		worker.mu.Unlock()
		writeError(response, http.StatusNotFound, "run not found")
		return
	}
	status := worker.statusLocked()
	worker.mu.Unlock()
	writeJSON(response, http.StatusOK, status)
}

func (worker *WorkerServer) handlePrepare(response http.ResponseWriter, request *http.Request) {
	var prepare PrepareRequest
	if err := decodeJSON(response, request, &prepare); err != nil {
		writeError(response, http.StatusBadRequest, "invalid request body")
		return
	}
	if prepare.ProtocolVersion != ProtocolVersion {
		writeError(response, http.StatusBadRequest, "unsupported protocol version")
		return
	}
	if err := validateIdentifier("run id", prepare.RunID); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := prepare.Plan.validate(prepare.RuntimeBindings); err != nil {
		writeError(response, http.StatusBadRequest, "execution plan validation failed")
		return
	}
	digest, err := planDigest(prepare.Plan)
	if err != nil {
		writeError(response, http.StatusBadRequest, "execution plan digest failed")
		return
	}
	loadEngine, err := engine.New(prepare.Plan.Config.EngineConfig(prepare.RuntimeBindings))
	if err != nil {
		writeError(response, http.StatusBadRequest, "execution plan compilation failed")
		return
	}

	worker.mu.Lock()
	if worker.state == WorkerScheduled || worker.state == WorkerRunning {
		worker.mu.Unlock()
		writeError(response, http.StatusConflict, "worker already has an active run")
		return
	}
	if worker.scheduleCancel != nil {
		worker.scheduleCancel()
	}
	worker.generation++
	worker.runID = prepare.RunID
	worker.planID = prepare.Plan.ID
	worker.planRevision = prepare.Plan.Revision
	worker.planDigest = digest
	worker.state = WorkerPrepared
	worker.loadEngine = loadEngine
	worker.scheduleCancel = nil
	worker.scheduledAt = time.Time{}
	worker.startedAt = time.Time{}
	worker.stoppedAt = time.Time{}
	worker.lastError = ""
	prepared := PrepareResponse{
		ProtocolVersion: ProtocolVersion,
		WorkerID:        worker.id,
		RunID:           worker.runID,
		PlanDigest:      worker.planDigest,
	}
	worker.mu.Unlock()
	writeJSON(response, http.StatusOK, prepared)
}

func (worker *WorkerServer) handleStart(response http.ResponseWriter, request *http.Request) {
	var start StartRequest
	if err := decodeJSON(response, request, &start); err != nil {
		writeError(response, http.StatusBadRequest, "invalid request body")
		return
	}
	if start.ProtocolVersion != ProtocolVersion {
		writeError(response, http.StatusBadRequest, "unsupported protocol version")
		return
	}

	worker.mu.Lock()
	if start.RunID == "" || start.RunID != worker.runID {
		worker.mu.Unlock()
		writeError(response, http.StatusNotFound, "run not found")
		return
	}
	if worker.state != WorkerPrepared {
		worker.mu.Unlock()
		writeError(response, http.StatusConflict, "run is not prepared")
		return
	}
	startAt := time.Unix(0, start.StartAtUnixNano)
	lead := startAt.Sub(worker.now())
	if lead < MinScheduledStartLead || lead > MaxScheduledStartLead {
		worker.mu.Unlock()
		writeError(response, http.StatusBadRequest, "scheduled start is outside the allowed window")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker.scheduleCancel = cancel
	worker.scheduledAt = startAt
	worker.state = WorkerScheduled
	generation := worker.generation
	loadEngine := worker.loadEngine
	runID := worker.runID
	status := worker.statusLocked()
	worker.mu.Unlock()
	go worker.runScheduled(ctx, generation, runID, startAt, loadEngine)
	writeJSON(response, http.StatusOK, status)
}

func (worker *WorkerServer) runScheduled(
	ctx context.Context,
	generation uint64,
	runID string,
	startAt time.Time,
	loadEngine *engine.Engine,
) {
	timer := time.NewTimer(max(startAt.Sub(worker.now()), 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	startedAt := worker.now()
	if err := loadEngine.Start(context.Background()); err != nil {
		worker.failRun(generation, runID, err)
		return
	}
	worker.mu.Lock()
	if worker.generation != generation || worker.runID != runID || worker.state != WorkerScheduled {
		worker.mu.Unlock()
		_ = loadEngine.Stop()
		return
	}
	worker.state = WorkerRunning
	worker.startedAt = startedAt
	worker.scheduleCancel = nil
	worker.mu.Unlock()

	<-loadEngine.Done()
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.generation == generation && worker.runID == runID && worker.state == WorkerRunning {
		worker.state = WorkerStopped
		worker.stoppedAt = worker.now()
	}
}

func (worker *WorkerServer) handleSnapshot(response http.ResponseWriter, request *http.Request) {
	runID := request.URL.Query().Get("runId")
	worker.mu.Lock()
	if runID == "" || runID != worker.runID {
		worker.mu.Unlock()
		writeError(response, http.StatusNotFound, "run not found")
		return
	}
	status := worker.statusLocked()
	loadEngine := worker.loadEngine
	worker.mu.Unlock()

	snapshot := engine.Snapshot{}
	var steps []engine.RequestStepSnapshot
	if loadEngine != nil {
		snapshot = loadEngine.Snapshot()
		steps = loadEngine.RequestStepSnapshots()
	}
	writeJSON(response, http.StatusOK, SnapshotResponse{Status: status, Snapshot: snapshot, RequestSteps: steps})
}

func (worker *WorkerServer) handleStop(response http.ResponseWriter, request *http.Request) {
	var stop StopRequest
	if err := decodeJSON(response, request, &stop); err != nil {
		writeError(response, http.StatusBadRequest, "invalid request body")
		return
	}
	if stop.ProtocolVersion != ProtocolVersion {
		writeError(response, http.StatusBadRequest, "unsupported protocol version")
		return
	}

	worker.mu.Lock()
	if stop.RunID == "" || stop.RunID != worker.runID {
		worker.mu.Unlock()
		writeError(response, http.StatusNotFound, "run not found")
		return
	}
	if worker.state.terminal() {
		status := worker.statusLocked()
		worker.mu.Unlock()
		writeJSON(response, http.StatusOK, status)
		return
	}
	if worker.scheduleCancel != nil {
		worker.scheduleCancel()
		worker.scheduleCancel = nil
	}
	loadEngine := worker.loadEngine
	generation := worker.generation
	wasRunning := worker.state == WorkerRunning
	if !wasRunning {
		worker.state = WorkerStopped
		worker.stoppedAt = worker.now()
		status := worker.statusLocked()
		worker.mu.Unlock()
		writeJSON(response, http.StatusOK, status)
		return
	}
	worker.mu.Unlock()

	if err := loadEngine.Stop(); err != nil && !errors.Is(err, engine.ErrNotRunning) {
		worker.failRun(generation, stop.RunID, err)
	}
	worker.mu.Lock()
	if worker.runID == stop.RunID && !worker.state.terminal() {
		worker.state = WorkerStopped
		worker.stoppedAt = worker.now()
	}
	status := worker.statusLocked()
	worker.mu.Unlock()
	writeJSON(response, http.StatusOK, status)
}

func (worker *WorkerServer) failRun(generation uint64, runID string, err error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.generation != generation || worker.runID != runID {
		return
	}
	worker.state = WorkerFailed
	worker.stoppedAt = worker.now()
	worker.lastError = err.Error()
	worker.scheduleCancel = nil
}

func (worker *WorkerServer) statusLocked() StatusResponse {
	return StatusResponse{
		ProtocolVersion: ProtocolVersion,
		WorkerID:        worker.id,
		ServerUnixNano:  worker.now().UnixNano(),
		RunID:           worker.runID,
		PlanID:          worker.planID,
		PlanRevision:    worker.planRevision,
		State:           worker.state,
		ScheduledAtNano: unixNano(worker.scheduledAt),
		StartedAtNano:   unixNano(worker.startedAt),
		StoppedAtNano:   unixNano(worker.stoppedAt),
		Error:           worker.lastError,
	}
}

func unixNano(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixNano()
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(response, request.Body, MaxControlBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeMethodNotAllowed(response http.ResponseWriter, method string) {
	response.Header().Set("Allow", method)
	writeError(response, http.StatusMethodNotAllowed, "method not allowed")
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, struct {
		Error string `json:"error"`
	}{Error: message})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
