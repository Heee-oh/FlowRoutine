package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const (
	vuScheduleInterval      = 10 * time.Millisecond
	arrivalScheduleInterval = time.Millisecond
)

type vuWorkerControl struct {
	active atomic.Bool
	busy   atomic.Bool
	wake   chan struct{}
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (e *Engine) runLoadProfile(ctx context.Context, cancel context.CancelFunc) {
	defer e.wg.Done()
	if e.cfg.profile.arrivalRate() {
		e.runArrivalProfile(ctx, cancel)
		return
	}
	e.runVUProfile(ctx, cancel)
}

func (e *Engine) runVUProfile(ctx context.Context, cancel context.CancelFunc) {
	profile := e.cfg.profile
	startedAt := time.Now()
	controls := make([]*vuWorkerControl, profile.maxWorkers)
	retireAt := make(map[int]time.Time)
	for index := range controls {
		control := &vuWorkerControl{wake: make(chan struct{}, 1)}
		controls[index] = control
		e.wg.Add(1)
		go e.runVUWorker(ctx, control, index)
	}
	currentTarget := 0
	reconcile := func(target int, now time.Time) {
		if target > currentTarget {
			for index := currentTarget; index < target; index++ {
				delete(retireAt, index)
				controls[index].setActive(true)
			}
		} else if target < currentTarget {
			deadline := now.Add(profile.gracefulStop)
			for index := target; index < currentTarget; index++ {
				controls[index].setActive(false)
				retireAt[index] = deadline
			}
		}
		currentTarget = target
	}

	reconcile(profile.virtualUsersAt(0), startedAt)
	ticker := time.NewTicker(vuScheduleInterval)
	defer ticker.Stop()
	var deadline <-chan time.Time
	var deadlineTimer *time.Timer
	if profile.duration > 0 {
		deadlineTimer = time.NewTimer(profile.duration)
		deadline = deadlineTimer.C
		defer deadlineTimer.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			reconcile(profile.virtualUsersAt(now.Sub(startedAt)), now)
			forceRetiredWorkers(controls, retireAt, now)
		case <-deadline:
			now := time.Now()
			reconcile(0, now)
			waitForVUWorkers(ctx, cancel, controls, retireAt, profile.gracefulStop)
			return
		}
	}
}

func (control *vuWorkerControl) setActive(active bool) {
	control.active.Store(active)
	if active {
		select {
		case control.wake <- struct{}{}:
		default:
		}
	}
}

func (control *vuWorkerControl) waitActive(ctx context.Context) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		if control.active.Load() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-control.wake:
		}
	}
}

func (control *vuWorkerControl) setCancel(cancel context.CancelFunc) {
	control.mu.Lock()
	control.cancel = cancel
	control.mu.Unlock()
}

func (control *vuWorkerControl) forceRetire() {
	control.mu.Lock()
	defer control.mu.Unlock()
	if !control.active.Load() && control.cancel != nil {
		control.cancel()
	}
}

func (e *Engine) runVUWorker(ctx context.Context, control *vuWorkerControl, index int) {
	defer e.wg.Done()
	runtime := e.newWorkerRuntime(index)
	workerCtx, workerCancel := context.WithCancel(ctx)
	control.setCancel(workerCancel)
	defer func() { workerCancel() }()
	resetWorkerContext := false
	for {
		if !control.waitActive(ctx) {
			return
		}
		if resetWorkerContext {
			workerCancel()
			workerCtx, workerCancel = context.WithCancel(ctx)
			control.setCancel(workerCancel)
			resetWorkerContext = false
		}
		control.busy.Store(true)
		if !control.active.Load() {
			control.busy.Store(false)
			continue
		}
		completed := e.runIteration(workerCtx, &runtime)
		control.busy.Store(false)
		if !completed && ctx.Err() != nil {
			return
		}
		resetWorkerContext = !completed
	}
}

func forceRetiredWorkers(controls []*vuWorkerControl, retireAt map[int]time.Time, now time.Time) {
	for index, deadline := range retireAt {
		if now.Before(deadline) {
			continue
		}
		controls[index].forceRetire()
		delete(retireAt, index)
	}
}

func waitForVUWorkers(
	ctx context.Context,
	cancel context.CancelFunc,
	controls []*vuWorkerControl,
	retireAt map[int]time.Time,
	gracefulStop time.Duration,
) {
	if allVUWorkersIdle(controls) || gracefulStop <= 0 {
		cancel()
		return
	}
	ticker := time.NewTicker(vuScheduleInterval)
	timer := time.NewTimer(gracefulStop)
	defer ticker.Stop()
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			forceRetiredWorkers(controls, retireAt, now)
			if allVUWorkersIdle(controls) {
				cancel()
				return
			}
		case <-timer.C:
			cancel()
			return
		}
	}
}

func allVUWorkersIdle(controls []*vuWorkerControl) bool {
	for _, control := range controls {
		if control.busy.Load() {
			return false
		}
	}
	return true
}

func (e *Engine) runArrivalProfile(ctx context.Context, cancel context.CancelFunc) {
	profile := e.cfg.profile
	startedAt := time.Now()
	ready := make(chan chan struct{}, profile.maxWorkers)
	workerDone := make(chan struct{}, profile.maxWorkers)
	stop := make(chan struct{})
	liveWorkers := 0
	nextWorker := 0
	idle := make([]chan struct{}, 0, profile.maxWorkers)

	startWorker := func(initialIteration bool) {
		jobs := make(chan struct{})
		e.wg.Add(1)
		go e.runArrivalWorker(ctx, stop, jobs, ready, nextWorker, initialIteration, workerDone)
		nextWorker++
		liveWorkers++
	}
	for range profile.preAllocatedVUs {
		startWorker(false)
	}
	for len(idle) < profile.preAllocatedVUs {
		select {
		case <-ctx.Done():
			return
		case jobs := <-ready:
			idle = append(idle, jobs)
		}
	}

	drainReady := func() {
		for {
			select {
			case jobs := <-ready:
				idle = append(idle, jobs)
			case <-workerDone:
				liveWorkers--
			default:
				return
			}
		}
	}
	dispatch := func(count uint64) {
		drainReady()
		for count > 0 {
			if len(idle) > 0 {
				last := len(idle) - 1
				jobs := idle[last]
				idle = idle[:last]
				select {
				case jobs <- struct{}{}:
				case <-ctx.Done():
					return
				}
				count--
				continue
			}
			if liveWorkers < profile.maxWorkers {
				startWorker(true)
				count--
				continue
			}
			e.stats.RecordDroppedIterations(count)
			return
		}
	}

	ticker := time.NewTicker(arrivalScheduleInterval)
	deadlineTimer := time.NewTimer(profile.duration)
	defer ticker.Stop()
	defer deadlineTimer.Stop()
	var scheduled uint64
	for {
		select {
		case <-ctx.Done():
			return
		case jobs := <-ready:
			idle = append(idle, jobs)
		case <-workerDone:
			liveWorkers--
		case now := <-ticker.C:
			expected := profile.arrivalsThrough(now.Sub(startedAt))
			if expected > scheduled {
				dispatch(expected - scheduled)
				scheduled = expected
			}
		case <-deadlineTimer.C:
			expected := profile.arrivalsThrough(profile.duration)
			if expected > scheduled {
				dispatch(expected - scheduled)
			}
			close(stop)
			waitForProfileWorkers(ctx, cancel, workerDone, liveWorkers, profile.gracefulStop)
			return
		}
	}
}

func (e *Engine) runArrivalWorker(
	ctx context.Context,
	stop <-chan struct{},
	jobs chan struct{},
	ready chan<- chan struct{},
	index int,
	initialIteration bool,
	done chan<- struct{},
) {
	defer e.wg.Done()
	defer func() { done <- struct{}{} }()
	runtime := e.newWorkerRuntime(index)
	if initialIteration && !e.runIteration(ctx, &runtime) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case ready <- jobs:
		}
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-jobs:
			if !e.runIteration(ctx, &runtime) {
				return
			}
		}
	}
}

func waitForProfileWorkers(
	ctx context.Context,
	cancel context.CancelFunc,
	workerDone <-chan struct{},
	liveWorkers int,
	gracefulStop time.Duration,
) {
	if liveWorkers <= 0 {
		cancel()
		return
	}
	if gracefulStop <= 0 {
		cancel()
		return
	}
	timer := time.NewTimer(gracefulStop)
	defer timer.Stop()
	for liveWorkers > 0 {
		select {
		case <-ctx.Done():
			return
		case <-workerDone:
			liveWorkers--
		case <-timer.C:
			cancel()
			return
		}
	}
	cancel()
}
