package distributed

import (
	"testing"
	"time"

	"flowroutine/internal/engine"
)

func TestShardConfigsPreservesLegacyLoadTotals(t *testing.T) {
	shards, err := ShardConfigs(engine.Config{
		URL:             "http://example.com",
		VirtualUsers:    10,
		Duration:        time.Second,
		MaxConnsPerHost: 12,
		RateLimitRPS:    7,
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 3 {
		t.Fatalf("got %d shards, want 3", len(shards))
	}
	var virtualUsers, rateLimit, connections int
	for _, shard := range shards {
		virtualUsers += shard.VirtualUsers
		rateLimit += shard.RateLimitRPS
		connections += shard.MaxConnsPerHost
	}
	if virtualUsers != 10 || rateLimit != 7 || connections != 12 {
		t.Fatalf("sharded totals changed: vus=%d rate=%d connections=%d", virtualUsers, rateLimit, connections)
	}
}

func TestShardConfigsPreservesRampingArrivalProfile(t *testing.T) {
	cfg := engine.Config{
		URL:             "http://example.com",
		MaxConnsPerHost: 20,
		Profile: &engine.LoadProfile{
			Mode:            engine.LoadModeRampingArrival,
			StartTarget:     10,
			PreAllocatedVUs: 9,
			MaxVUs:          15,
			GracefulStop:    time.Second,
			Stages: []engine.LoadStage{
				{Duration: time.Second, Target: 20},
				{Duration: time.Second, Target: 5},
			},
		},
	}
	shards, err := ShardConfigs(cfg, 4)
	if err != nil {
		t.Fatal(err)
	}
	var start, first, second, preAllocated, maximum int
	for _, shard := range shards {
		if err := engine.ValidateConfig(shard); err != nil {
			t.Fatal(err)
		}
		start += shard.Profile.StartTarget
		first += shard.Profile.Stages[0].Target
		second += shard.Profile.Stages[1].Target
		preAllocated += shard.Profile.PreAllocatedVUs
		maximum += shard.Profile.MaxVUs
	}
	if start != 10 || first != 20 || second != 5 || preAllocated != 9 || maximum != 15 {
		t.Fatalf("arrival profile totals changed: start=%d stages=%d/%d pre=%d max=%d", start, first, second, preAllocated, maximum)
	}
}

func TestShardConfigsUsesOnlyWorkersWithPositiveCapacity(t *testing.T) {
	shards, err := ShardConfigs(engine.Config{
		URL:             "http://example.com",
		VirtualUsers:    2,
		MaxConnsPerHost: 100,
	}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 2 || shards[0].VirtualUsers != 1 || shards[1].VirtualUsers != 1 {
		t.Fatalf("unexpected active shards: %+v", shards)
	}
}
