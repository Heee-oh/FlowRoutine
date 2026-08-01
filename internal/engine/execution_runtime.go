package engine

import "context"

func (e *Engine) runPlannedIteration(ctx context.Context, runtime *workerRuntime) bool {
	plan := e.cfg.executionPlan
	clear(runtime.loopIterations)
	runtime.activeRoutes = runtime.activeRoutes[:0]
	sampleLatency := runtime.nextLatencySample(e.cfg.latencySampleRate)
	lastStatus := 0
	lastRequestMetricsIndex := -1
	programCounter := plan.entry
	for transitions := 0; programCounter >= 0 && transitions < plan.maxTransitions; transitions++ {
		instruction := &plan.steps[programCounter]
		switch instruction.kind {
		case compiledExecutionScenario:
			outcome := e.runStep(
				ctx,
				runtime,
				sampleLatency,
				&lastStatus,
				&lastRequestMetricsIndex,
				&e.cfg.steps[instruction.scenarioIndex],
			)
			if outcome == stepCanceled {
				return false
			}
			if outcome == stepStopIteration {
				return true
			}
			programCounter = instruction.next
		case compiledExecutionBranch:
			route := selectExecutionRoute(instruction, runtime.index, runtime.iterationIndex)
			if shard := e.stats.branchRouteShard(route.metricsIndex, runtime.index); shard != nil {
				shard.recordSelection()
			}
			runtime.variables.pushBranch(route.scopeKey)
			runtime.activeRoutes = append(runtime.activeRoutes, activeBranchRoute{
				join: instruction.join, metricsIndex: route.metricsIndex,
			})
			programCounter = route.target
		case compiledExecutionJoin:
			if len(runtime.activeRoutes) == 0 || runtime.activeRoutes[len(runtime.activeRoutes)-1].join != programCounter {
				return true
			}
			runtime.activeRoutes = runtime.activeRoutes[:len(runtime.activeRoutes)-1]
			runtime.variables.popBranch()
			programCounter = instruction.next
		case compiledExecutionLoop:
			if runtime.loopIterations[programCounter] < instruction.maxIterations {
				runtime.loopIterations[programCounter]++
				programCounter = instruction.body
			} else {
				runtime.loopIterations[programCounter] = 0
				programCounter = instruction.exit
			}
		}
	}
	return true
}

func selectExecutionRoute(step *compiledExecutionStep, workerIndex int, iterationIndex uint64) *compiledExecutionRoute {
	selection := deterministicExecutionValue(workerIndex, iterationIndex, step.id) % step.routeWeight
	for index := range step.routes {
		if selection < step.routes[index].weight {
			return &step.routes[index]
		}
		selection -= step.routes[index].weight
	}
	return &step.routes[len(step.routes)-1]
}

func deterministicExecutionValue(workerIndex int, iterationIndex uint64, branchID string) uint64 {
	value := uint64(14695981039346656037)
	for index := 0; index < len(branchID); index++ {
		value ^= uint64(branchID[index])
		value *= 1099511628211
	}
	value ^= uint64(workerIndex + 1)
	value *= 0x9e3779b97f4a7c15
	value ^= iterationIndex + 0x9e3779b97f4a7c15
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func (runtime *workerRuntime) recordBranchRequest(stats *AtomicStats, success bool) {
	for _, active := range runtime.activeRoutes {
		if shard := stats.branchRouteShard(active.metricsIndex, runtime.index); shard != nil {
			shard.recordRequest(success)
		}
	}
}
