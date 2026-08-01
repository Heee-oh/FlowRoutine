package engine

import (
	"strings"
	"testing"
	"time"
)

func TestLoadProfileTargetsAtDeterministicStageBoundaries(t *testing.T) {
	profile, err := compileLoadProfile(Config{
		Profile: &LoadProfile{
			Mode:        LoadModeRampingVUs,
			StartTarget: 0,
			Stages: []LoadStage{
				{Duration: time.Second, Target: 10},
				{Duration: 2 * time.Second, Target: 20},
			},
		},
	}, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		elapsed time.Duration
		want    int
	}{
		{elapsed: 0, want: 0},
		{elapsed: 500 * time.Millisecond, want: 5},
		{elapsed: time.Second, want: 10},
		{elapsed: 2 * time.Second, want: 15},
		{elapsed: 3 * time.Second, want: 20},
		{elapsed: 4 * time.Second, want: 20},
	}
	for _, test := range tests {
		if got := profile.virtualUsersAt(test.elapsed); got != test.want {
			t.Fatalf("target at %s = %d, want %d", test.elapsed, got, test.want)
		}
	}
}

func TestArrivalProfileIntegratesRampingRates(t *testing.T) {
	profile, err := compileLoadProfile(Config{
		Profile: &LoadProfile{
			Mode:            LoadModeRampingArrival,
			StartTarget:     10,
			PreAllocatedVUs: 1,
			MaxVUs:          2,
			Stages: []LoadStage{
				{Duration: time.Second, Target: 20},
				{Duration: time.Second, Target: 20},
			},
		},
	}, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		elapsed time.Duration
		want    uint64
	}{
		{elapsed: 500 * time.Millisecond, want: 6},
		{elapsed: time.Second, want: 15},
		{elapsed: 1500 * time.Millisecond, want: 25},
		{elapsed: 2 * time.Second, want: 35},
		{elapsed: 3 * time.Second, want: 35},
	}
	for _, test := range tests {
		if got := profile.arrivalsThrough(test.elapsed); got != test.want {
			t.Fatalf("arrivals through %s = %d, want %d", test.elapsed, got, test.want)
		}
	}
}

func TestLoadProfileValidationRejectsUnsafeCombinations(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		message string
	}{
		{
			name: "constant target changes",
			config: Config{Profile: &LoadProfile{
				Mode:        LoadModeConstantVUs,
				StartTarget: 2,
				Stages:      []LoadStage{{Duration: time.Second, Target: 3}},
			}},
			message: "constant-vus",
		},
		{
			name: "arrival pool inverted",
			config: Config{Profile: &LoadProfile{
				Mode:            LoadModeConstantArrival,
				StartTarget:     10,
				Stages:          []LoadStage{{Duration: time.Second, Target: 10}},
				PreAllocatedVUs: 3,
				MaxVUs:          2,
			}},
			message: "max virtual users",
		},
		{
			name: "double throttling",
			config: Config{
				RateLimitRPS: 1,
				Profile: &LoadProfile{
					Mode:            LoadModeConstantArrival,
					StartTarget:     10,
					Stages:          []LoadStage{{Duration: time.Second, Target: 10}},
					PreAllocatedVUs: 1,
					MaxVUs:          1,
				},
			},
			message: "cannot be combined",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := compileLoadProfile(test.config, 1, time.Second)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v, want error containing %q", err, test.message)
			}
		})
	}
}
