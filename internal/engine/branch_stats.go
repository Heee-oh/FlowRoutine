package engine

import "sync/atomic"

const (
	maxBranchRouteStatsShards      = 4
	branchRouteStatsEstimatedBytes = 64
)

type atomicBranchRouteStats struct {
	branchID string
	routeID  string
	name     string
	shards   []branchRouteStatsShard
}

type branchRouteStatsShard struct {
	selections atomic.Uint64
	total      atomic.Uint64
	success    atomic.Uint64
	failed     atomic.Uint64
	_          [32]byte
}

type BranchRouteSnapshot struct {
	BranchID   string
	RouteID    string
	Name       string
	Selections uint64
	Total      uint64
	Success    uint64
	Failed     uint64
}

func (s *AtomicStats) initBranchRoutes(virtualUsers int, descriptors []branchRouteDescriptor) {
	shards := min(max(1, virtualUsers), maxBranchRouteStatsShards)
	s.branchRoutes = make([]atomicBranchRouteStats, len(descriptors))
	for index, descriptor := range descriptors {
		s.branchRoutes[index] = atomicBranchRouteStats{
			branchID: descriptor.branchID,
			routeID:  descriptor.routeID,
			name:     descriptor.name,
			shards:   make([]branchRouteStatsShard, shards),
		}
	}
}

func (s *AtomicStats) branchRouteShard(routeIndex int, workerIndex int) *branchRouteStatsShard {
	if routeIndex < 0 || routeIndex >= len(s.branchRoutes) {
		return nil
	}
	route := &s.branchRoutes[routeIndex]
	return &route.shards[workerIndex%len(route.shards)]
}

func (s *AtomicStats) BranchRouteSnapshots() []BranchRouteSnapshot {
	if len(s.branchRoutes) == 0 {
		return nil
	}
	snapshots := make([]BranchRouteSnapshot, len(s.branchRoutes))
	for index := range s.branchRoutes {
		route := &s.branchRoutes[index]
		snapshot := BranchRouteSnapshot{BranchID: route.branchID, RouteID: route.routeID, Name: route.name}
		for shardIndex := range route.shards {
			shard := &route.shards[shardIndex]
			snapshot.Selections += shard.selections.Load()
			snapshot.Total += shard.total.Load()
			snapshot.Success += shard.success.Load()
			snapshot.Failed += shard.failed.Load()
		}
		snapshots[index] = snapshot
	}
	return snapshots
}

func (s *atomicBranchRouteStats) reset() {
	for index := range s.shards {
		s.shards[index].selections.Store(0)
		s.shards[index].total.Store(0)
		s.shards[index].success.Store(0)
		s.shards[index].failed.Store(0)
	}
}

func (s *branchRouteStatsShard) recordSelection() {
	s.selections.Add(1)
}

func (s *branchRouteStatsShard) recordRequest(success bool) {
	s.total.Add(1)
	if success {
		s.success.Add(1)
		return
	}
	s.failed.Add(1)
}

func EstimateBranchMetricsBytes(routes int, virtualUsers int) uint64 {
	if routes <= 0 {
		return 0
	}
	shards := min(max(1, virtualUsers), maxBranchRouteStatsShards)
	return uint64(routes * shards * branchRouteStatsEstimatedBytes)
}
