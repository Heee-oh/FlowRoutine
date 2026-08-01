package distributed

import (
	"errors"
	"fmt"

	"flowroutine/internal/engine"
)

func ShardConfigs(cfg engine.Config, workerCount int) ([]engine.Config, error) {
	if workerCount < 1 {
		return nil, errors.New("worker count must be greater than zero")
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	active := workerCount
	maxConnections := cfg.MaxConnsPerHost
	if maxConnections == 0 {
		maxConnections = engine.DefaultMaxConnsPerHost
	}
	active = min(active, maxConnections)
	if cfg.RateLimitRPS > 0 {
		active = min(active, cfg.RateLimitRPS)
	}

	if cfg.Profile == nil || cfg.Profile.Mode == "" {
		virtualUsers := cfg.VirtualUsers
		if virtualUsers == 0 {
			virtualUsers = engine.DefaultVirtualUsers
		}
		active = min(active, virtualUsers)
	} else {
		peakTarget := cfg.Profile.StartTarget
		for _, stage := range cfg.Profile.Stages {
			peakTarget = max(peakTarget, stage.Target)
		}
		active = min(active, peakTarget)
		if arrivalMode(cfg.Profile.Mode) {
			active = min(active, cfg.Profile.PreAllocatedVUs)
			active = min(active, cfg.Profile.MaxVUs)
		}
	}
	if active < 1 {
		return nil, errors.New("config has no positive worker capacity")
	}

	shards := make([]engine.Config, active)
	for index := range shards {
		shard := cloneEngineConfig(cfg)
		shard.MaxConnsPerHost = splitInteger(maxConnections, index, active)
		if cfg.RateLimitRPS > 0 {
			shard.RateLimitRPS = splitInteger(cfg.RateLimitRPS, index, active)
		}
		if cfg.Profile == nil || cfg.Profile.Mode == "" {
			virtualUsers := cfg.VirtualUsers
			if virtualUsers == 0 {
				virtualUsers = engine.DefaultVirtualUsers
			}
			shard.VirtualUsers = splitInteger(virtualUsers, index, active)
		} else {
			shard.Profile.StartTarget = splitInteger(cfg.Profile.StartTarget, index, active)
			for stageIndex := range shard.Profile.Stages {
				shard.Profile.Stages[stageIndex].Target = splitInteger(
					cfg.Profile.Stages[stageIndex].Target,
					index,
					active,
				)
			}
			if arrivalMode(cfg.Profile.Mode) {
				shard.Profile.PreAllocatedVUs = splitInteger(cfg.Profile.PreAllocatedVUs, index, active)
				shard.Profile.MaxVUs = splitInteger(cfg.Profile.MaxVUs, index, active)
			}
		}
		if err := engine.ValidateConfig(shard); err != nil {
			return nil, fmt.Errorf("invalid worker shard %d: %w", index+1, err)
		}
		shards[index] = shard
	}
	return shards, nil
}

func splitInteger(total int, index int, count int) int {
	value := total / count
	if index < total%count {
		value++
	}
	return value
}

func arrivalMode(mode engine.LoadMode) bool {
	return mode == engine.LoadModeConstantArrival || mode == engine.LoadModeRampingArrival
}

func cloneEngineConfig(cfg engine.Config) engine.Config {
	cloned := cfg
	cloned.Body = append([]byte(nil), cfg.Body...)
	cloned.Headers = append([]engine.Header(nil), cfg.Headers...)
	cloned.RuntimeVariables = cloneStrings(cfg.RuntimeVariables)
	if cfg.Profile != nil {
		profile := *cfg.Profile
		profile.Stages = append([]engine.LoadStage(nil), cfg.Profile.Stages...)
		cloned.Profile = &profile
	}
	cloned.ScenarioSteps = make([]engine.ScenarioStep, len(cfg.ScenarioSteps))
	for index, step := range cfg.ScenarioSteps {
		cloned.ScenarioSteps[index] = step
		cloned.ScenarioSteps[index].Headers = append([]engine.Header(nil), step.Headers...)
		cloned.ScenarioSteps[index].Body = append([]byte(nil), step.Body...)
		cloned.ScenarioSteps[index].Captures = append([]engine.VariableCapture(nil), step.Captures...)
	}
	if cfg.ExecutionPlan != nil {
		plan := *cfg.ExecutionPlan
		plan.Steps = make([]engine.ExecutionPlanStep, len(cfg.ExecutionPlan.Steps))
		for index, step := range cfg.ExecutionPlan.Steps {
			plan.Steps[index] = step
			plan.Steps[index].Routes = append([]engine.ExecutionRoute(nil), step.Routes...)
		}
		cloned.ExecutionPlan = &plan
	}
	return cloned
}
