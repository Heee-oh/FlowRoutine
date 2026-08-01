package distributed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

const DefaultStartLead = 750 * time.Millisecond

type Coordinator struct {
	clients   []*WorkerClient
	startLead time.Duration
	now       func() time.Time
}

type CoordinatorOption func(*Coordinator) error

func WithStartLead(lead time.Duration) CoordinatorOption {
	return func(coordinator *Coordinator) error {
		if lead < 2*MinScheduledStartLead || lead > MaxScheduledStartLead {
			return fmt.Errorf("start lead must be between %s and %s", 2*MinScheduledStartLead, MaxScheduledStartLead)
		}
		coordinator.startLead = lead
		return nil
	}
}

func NewCoordinator(targets []WorkerTarget, options ...CoordinatorOption) (*Coordinator, error) {
	if len(targets) == 0 {
		return nil, errors.New("at least one worker is required")
	}
	coordinator := &Coordinator{startLead: DefaultStartLead, now: time.Now}
	identities := make(map[string]struct{}, len(targets))
	urls := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if _, exists := identities[target.ID]; exists {
			coordinator.Close()
			return nil, fmt.Errorf("worker id %q is duplicated", target.ID)
		}
		if _, exists := urls[target.URL]; exists {
			coordinator.Close()
			return nil, fmt.Errorf("worker URL %q is duplicated", target.URL)
		}
		client, err := NewWorkerClient(target)
		if err != nil {
			coordinator.Close()
			return nil, fmt.Errorf("worker %q: %w", target.ID, err)
		}
		identities[target.ID] = struct{}{}
		urls[target.URL] = struct{}{}
		coordinator.clients = append(coordinator.clients, client)
	}
	for _, option := range options {
		if err := option(coordinator); err != nil {
			coordinator.Close()
			return nil, err
		}
	}
	return coordinator, nil
}

func (coordinator *Coordinator) Close() {
	for _, client := range coordinator.clients {
		client.Close()
	}
}

func (coordinator *Coordinator) Start(
	ctx context.Context,
	plan ExecutionPlan,
	runtimeBindings map[string]string,
) (*Run, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := plan.validate(runtimeBindings); err != nil {
		return nil, err
	}
	config := plan.Config.EngineConfig(runtimeBindings)
	shards, err := ShardConfigs(config, len(coordinator.clients))
	if err != nil {
		return nil, err
	}
	clients := coordinator.clients[:len(shards)]
	runID, err := newRunID()
	if err != nil {
		return nil, fmt.Errorf("create run id: %w", err)
	}

	clocks, err := coordinator.probeClocks(ctx, clients)
	if err != nil {
		return nil, err
	}
	maximumRoundTrip := time.Duration(0)
	for _, clock := range clocks {
		maximumRoundTrip = max(maximumRoundTrip, clock.roundTrip)
	}
	lead := max(coordinator.startLead, 2*MinScheduledStartLead+maximumRoundTrip)
	startAt := coordinator.now().Add(lead)

	if err := parallelWorkers(ctx, len(clients), func(index int) error {
		workerPlan := plan
		workerPlan.Config = NewPlanConfig(shards[index])
		expectedDigest, err := planDigest(workerPlan)
		if err != nil {
			return err
		}
		response, err := clients[index].Prepare(ctx, PrepareRequest{
			ProtocolVersion: ProtocolVersion,
			RunID:           runID,
			Plan:            workerPlan,
			RuntimeBindings: cloneStrings(runtimeBindings),
		})
		if err != nil {
			return err
		}
		if response.PlanDigest != expectedDigest {
			return errors.New("worker returned a mismatched plan digest")
		}
		return nil
	}); err != nil {
		coordinator.stopPrepared(runID, clients)
		return nil, fmt.Errorf("prepare distributed run: %w", err)
	}

	if err := parallelWorkers(ctx, len(clients), func(index int) error {
		_, err := clients[index].Start(ctx, StartRequest{
			ProtocolVersion: ProtocolVersion,
			RunID:           runID,
			StartAtUnixNano: startAt.Add(clocks[index].offset).UnixNano(),
		})
		return err
	}); err != nil {
		coordinator.stopPrepared(runID, clients)
		return nil, fmt.Errorf("schedule distributed run: %w", err)
	}

	run := &Run{
		id:        runID,
		planID:    plan.ID,
		revision:  plan.Revision,
		startedAt: startAt,
		now:       coordinator.now,
		workers:   make([]runWorker, len(clients)),
	}
	for index, client := range clients {
		run.workers[index] = runWorker{
			client:      client,
			clockOffset: clocks[index].offset,
			roundTrip:   clocks[index].roundTrip,
			health: WorkerHealth{
				ID:                 client.ID(),
				Reachable:          true,
				State:              WorkerScheduled,
				ClockOffsetNano:    clocks[index].offset.Nanoseconds(),
				ClockRoundTripNano: clocks[index].roundTrip.Nanoseconds(),
				ScheduledUnixNano:  startAt.UnixNano(),
			},
		}
	}
	return run, nil
}

type clockSample struct {
	offset    time.Duration
	roundTrip time.Duration
}

func (coordinator *Coordinator) probeClocks(ctx context.Context, clients []*WorkerClient) ([]clockSample, error) {
	clocks := make([]clockSample, len(clients))
	err := parallelWorkers(ctx, len(clients), func(index int) error {
		before := coordinator.now()
		status, err := clients[index].Status(ctx, "")
		after := coordinator.now()
		if err != nil {
			return err
		}
		roundTrip := after.Sub(before)
		midpoint := before.Add(roundTrip / 2)
		clocks[index] = clockSample{
			offset:    time.Unix(0, status.ServerUnixNano).Sub(midpoint),
			roundTrip: roundTrip,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("synchronize worker clocks: %w", err)
	}
	return clocks, nil
}

func (coordinator *Coordinator) stopPrepared(runID string, clients []*WorkerClient) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultControlTimeout)
	defer cancel()
	_ = parallelWorkers(ctx, len(clients), func(index int) error {
		_, err := clients[index].Stop(ctx, StopRequest{ProtocolVersion: ProtocolVersion, RunID: runID})
		return err
	})
}

func parallelWorkers(ctx context.Context, count int, operation func(index int) error) error {
	var wait sync.WaitGroup
	errorsByIndex := make([]error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			if err := ctx.Err(); err != nil {
				errorsByIndex[index] = err
				return
			}
			errorsByIndex[index] = operation(index)
		}(index)
	}
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			return fmt.Errorf("worker %d: %w", index+1, err)
		}
	}
	return nil
}

func newRunID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
