package engine

import (
	"fmt"
	"math"
	"time"
)

const (
	MaxLoadStages              = 64
	MaxLoadProfileVirtualUsers = 100_000
)

type LoadMode string

const (
	LoadModeConstantVUs     LoadMode = "constant-vus"
	LoadModeRampingVUs      LoadMode = "ramping-vus"
	LoadModeConstantArrival LoadMode = "constant-arrival-rate"
	LoadModeRampingArrival  LoadMode = "ramping-arrival-rate"
)

type LoadStage struct {
	Duration time.Duration
	Target   int
}

type LoadProfile struct {
	Mode            LoadMode
	StartTarget     int
	Stages          []LoadStage
	PreAllocatedVUs int
	MaxVUs          int
	GracefulStop    time.Duration
}

type compiledLoadProfile struct {
	mode            LoadMode
	startTarget     int
	stages          []LoadStage
	duration        time.Duration
	preAllocatedVUs int
	maxWorkers      int
	gracefulStop    time.Duration
}

func compileLoadProfile(cfg Config, virtualUsers int, requestTimeout time.Duration) (compiledLoadProfile, error) {
	if cfg.Profile == nil || cfg.Profile.Mode == "" {
		return compileLegacyLoadProfile(cfg, virtualUsers), nil
	}

	profile := *cfg.Profile
	if profile.GracefulStop < 0 {
		return compiledLoadProfile{}, fmt.Errorf("graceful stop must be >= 0: %s", profile.GracefulStop)
	}
	if profile.GracefulStop == 0 {
		profile.GracefulStop = requestTimeout
	}
	if len(profile.Stages) == 0 || len(profile.Stages) > MaxLoadStages {
		return compiledLoadProfile{}, fmt.Errorf("load profile must have between 1 and %d stages", MaxLoadStages)
	}

	totalDuration := time.Duration(0)
	maxTarget := profile.StartTarget
	positiveTarget := profile.StartTarget > 0
	for index, stage := range profile.Stages {
		if stage.Duration <= 0 {
			return compiledLoadProfile{}, fmt.Errorf("load profile stage %d duration must be > 0", index+1)
		}
		if stage.Target < 0 {
			return compiledLoadProfile{}, fmt.Errorf("load profile stage %d target must be >= 0", index+1)
		}
		if totalDuration > time.Duration(math.MaxInt64)-stage.Duration {
			return compiledLoadProfile{}, fmt.Errorf("load profile duration is too large")
		}
		totalDuration += stage.Duration
		maxTarget = max(maxTarget, stage.Target)
		positiveTarget = positiveTarget || stage.Target > 0
	}
	if profile.StartTarget < 0 {
		return compiledLoadProfile{}, fmt.Errorf("load profile start target must be >= 0")
	}
	if !positiveTarget {
		return compiledLoadProfile{}, fmt.Errorf("load profile must have a positive target")
	}

	compiled := compiledLoadProfile{
		mode:         profile.Mode,
		startTarget:  profile.StartTarget,
		stages:       append([]LoadStage(nil), profile.Stages...),
		duration:     totalDuration,
		gracefulStop: profile.GracefulStop,
	}
	switch profile.Mode {
	case LoadModeConstantVUs:
		if profile.StartTarget < 1 || len(profile.Stages) != 1 || profile.Stages[0].Target != profile.StartTarget {
			return compiledLoadProfile{}, fmt.Errorf("constant-vus requires one stage matching a positive start target")
		}
		if maxTarget > MaxLoadProfileVirtualUsers {
			return compiledLoadProfile{}, fmt.Errorf("virtual-user target must be <= %d", MaxLoadProfileVirtualUsers)
		}
		compiled.maxWorkers = maxTarget
	case LoadModeRampingVUs:
		if maxTarget > MaxLoadProfileVirtualUsers {
			return compiledLoadProfile{}, fmt.Errorf("virtual-user target must be <= %d", MaxLoadProfileVirtualUsers)
		}
		compiled.maxWorkers = maxTarget
	case LoadModeConstantArrival:
		if profile.StartTarget < 1 || len(profile.Stages) != 1 || profile.Stages[0].Target != profile.StartTarget {
			return compiledLoadProfile{}, fmt.Errorf("constant-arrival-rate requires one stage matching a positive start target")
		}
		if maxTarget > MaxRateLimitRPS {
			return compiledLoadProfile{}, fmt.Errorf("arrival-rate target must be <= %d", MaxRateLimitRPS)
		}
		if err := validateArrivalWorkers(profile); err != nil {
			return compiledLoadProfile{}, err
		}
		if cfg.RateLimitRPS != 0 {
			return compiledLoadProfile{}, fmt.Errorf("rate limit rps cannot be combined with an arrival-rate profile")
		}
		compiled.preAllocatedVUs = profile.PreAllocatedVUs
		compiled.maxWorkers = profile.MaxVUs
	case LoadModeRampingArrival:
		if maxTarget > MaxRateLimitRPS {
			return compiledLoadProfile{}, fmt.Errorf("arrival-rate target must be <= %d", MaxRateLimitRPS)
		}
		if err := validateArrivalWorkers(profile); err != nil {
			return compiledLoadProfile{}, err
		}
		if cfg.RateLimitRPS != 0 {
			return compiledLoadProfile{}, fmt.Errorf("rate limit rps cannot be combined with an arrival-rate profile")
		}
		compiled.preAllocatedVUs = profile.PreAllocatedVUs
		compiled.maxWorkers = profile.MaxVUs
	default:
		return compiledLoadProfile{}, fmt.Errorf("unsupported load profile mode %q", profile.Mode)
	}
	return compiled, nil
}

func validateArrivalWorkers(profile LoadProfile) error {
	if profile.PreAllocatedVUs < 1 || profile.PreAllocatedVUs > MaxLoadProfileVirtualUsers {
		return fmt.Errorf("pre-allocated virtual users must be between 1 and %d", MaxLoadProfileVirtualUsers)
	}
	if profile.MaxVUs < profile.PreAllocatedVUs || profile.MaxVUs > MaxLoadProfileVirtualUsers {
		return fmt.Errorf("max virtual users must be between pre-allocated virtual users and %d", MaxLoadProfileVirtualUsers)
	}
	return nil
}

func compileLegacyLoadProfile(cfg Config, virtualUsers int) compiledLoadProfile {
	profile := compiledLoadProfile{
		mode:        LoadModeConstantVUs,
		startTarget: virtualUsers,
		duration:    cfg.Duration,
		maxWorkers:  virtualUsers,
	}
	if cfg.RampUp > 0 && virtualUsers > 1 {
		profile.mode = LoadModeRampingVUs
		profile.startTarget = 1
		profile.stages = []LoadStage{{Duration: cfg.RampUp, Target: virtualUsers}}
	}
	return profile
}

func (profile compiledLoadProfile) arrivalRate() bool {
	return profile.mode == LoadModeConstantArrival || profile.mode == LoadModeRampingArrival
}

func (profile compiledLoadProfile) targetAt(elapsed time.Duration) float64 {
	if elapsed <= 0 || len(profile.stages) == 0 {
		return float64(profile.startTarget)
	}
	remaining := elapsed
	from := float64(profile.startTarget)
	for _, stage := range profile.stages {
		to := float64(stage.Target)
		if remaining < stage.Duration {
			fraction := float64(remaining) / float64(stage.Duration)
			return from + (to-from)*fraction
		}
		remaining -= stage.Duration
		from = to
	}
	return from
}

func (profile compiledLoadProfile) virtualUsersAt(elapsed time.Duration) int {
	target := int(math.Round(profile.targetAt(elapsed)))
	if target < 0 {
		return 0
	}
	if target > profile.maxWorkers {
		return profile.maxWorkers
	}
	return target
}

func (profile compiledLoadProfile) arrivalsThrough(elapsed time.Duration) uint64 {
	if elapsed <= 0 {
		return 0
	}
	if profile.duration > 0 && elapsed > profile.duration {
		elapsed = profile.duration
	}
	remaining := elapsed
	from := float64(profile.startTarget)
	arrivals := 0.0
	for _, stage := range profile.stages {
		segment := min(remaining, stage.Duration)
		if segment <= 0 {
			break
		}
		fraction := float64(segment) / float64(stage.Duration)
		to := from + (float64(stage.Target)-from)*fraction
		arrivals += (from + to) * 0.5 * segment.Seconds()
		remaining -= segment
		from = float64(stage.Target)
	}
	if remaining > 0 {
		arrivals += from * remaining.Seconds()
	}
	if arrivals <= 0 {
		return 0
	}
	return uint64(math.Floor(arrivals + 1e-9))
}
