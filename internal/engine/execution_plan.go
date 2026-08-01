package engine

import (
	"errors"
	"fmt"
	"strings"
)

const maxBranchRouteWeight = 1_000_000

type compiledExecutionStepKind uint8

const (
	compiledExecutionScenario compiledExecutionStepKind = iota
	compiledExecutionBranch
	compiledExecutionJoin
	compiledExecutionLoop
)

type compiledExecutionPlan struct {
	entry          int
	steps          []compiledExecutionStep
	maxTransitions int
	routeCount     int
}

type compiledExecutionStep struct {
	id            string
	kind          compiledExecutionStepKind
	scenarioIndex int
	next          int
	routes        []compiledExecutionRoute
	routeWeight   uint64
	join          int
	body          int
	exit          int
	maxIterations int
}

type compiledExecutionRoute struct {
	id           string
	name         string
	target       int
	weight       uint64
	metricsIndex int
	scopeKey     string
}

type branchRouteDescriptor struct {
	branchID string
	routeID  string
	name     string
}

func compilePlannedScenarioSteps(cfg Config, defaultMethod string) ([]compiledStep, []compiledClient, error) {
	steps := cfg.ScenarioSteps
	if len(steps) == 0 {
		return nil, nil, errors.New("execution plan requires scenario steps")
	}
	if len(steps) > MaxScenarioSteps {
		return nil, nil, fmt.Errorf("scenario must have at most %d steps", MaxScenarioSteps)
	}

	clientIndexes := make(map[string]int)
	variableScopes := make(map[string]VariableScope, len(cfg.RuntimeVariables))
	for name := range cfg.RuntimeVariables {
		variableScopes[name] = "runtime"
	}
	stepIDs := make(map[string]struct{}, len(steps))
	clients := make([]compiledClient, 0, len(steps))
	compiled := make([]compiledStep, 0, len(steps))
	requestIndex := 0
	assertionResultIndex := 0
	for index, step := range steps {
		id, name, err := compileScenarioStepIdentity(step, index)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := stepIDs[id]; exists {
			return nil, nil, fmt.Errorf("scenario step %d id %q is duplicated", index+1, id)
		}
		stepIDs[id] = struct{}{}
		next := compiledStep{id: id, name: name}
		switch step.Kind {
		case "", StepRequest:
			target, err := parseTarget(step.URL)
			if err != nil {
				return nil, nil, err
			}
			clientKey := target.Scheme + "://" + targetAddr(target)
			clientIndex, ok := clientIndexes[clientKey]
			if !ok {
				clientIndex = len(clientIndexes)
				clientIndexes[clientKey] = clientIndex
				clients = append(clients, compiledClient{addr: targetAddr(target), isTLS: target.Scheme == "https"})
			}
			request, err := compileRequestStep(step, target, defaultMethod, clientIndex)
			if err != nil {
				return nil, nil, err
			}
			request.metricsIndex = requestIndex
			requestIndex++
			for _, capture := range request.captures {
				if scope, exists := variableScopes[capture.name]; exists && scope != capture.scope {
					return nil, nil, fmt.Errorf(
						"scenario step %d capture %q changes scope from %q to %q",
						index+1,
						capture.name,
						scope,
						capture.scope,
					)
				}
				variableScopes[capture.name] = capture.scope
			}
			next.kind = compiledRequest
			next.request = request
		case StepDelay:
			if step.Delay < 0 {
				return nil, nil, fmt.Errorf("delay must be >= 0: %s", step.Delay)
			}
			next.kind = compiledDelay
			next.delay = step.Delay
		case StepAssert, StepAssertStatus:
			assertion, err := compileAssertion(step)
			if err != nil {
				return nil, nil, fmt.Errorf("scenario step %d assertion: %w", index+1, err)
			}
			assertion.resultIndex = assertionResultIndex
			assertionResultIndex++
			next.kind = compiledAssert
			next.assertion = assertion
		default:
			return nil, nil, fmt.Errorf("unsupported scenario step kind: %s", step.Kind)
		}
		compiled = append(compiled, next)
	}
	return compiled, clients, nil
}

func compileExecutionPlan(
	plan *ExecutionPlan,
	scenarioSteps []compiledStep,
	runtimeVariables map[string]string,
) (*compiledExecutionPlan, error) {
	if plan == nil {
		return nil, nil
	}
	if plan.SchemaVersion != ExecutionPlanSchemaVersion {
		return nil, fmt.Errorf("unsupported execution plan schema version %d", plan.SchemaVersion)
	}
	if len(plan.Steps) == 0 || len(plan.Steps) > MaxExecutionPlanSteps {
		return nil, fmt.Errorf("execution plan must have between 1 and %d steps", MaxExecutionPlanSteps)
	}

	scenarioByID := make(map[string]int, len(scenarioSteps))
	for index := range scenarioSteps {
		scenarioByID[scenarioSteps[index].id] = index
	}
	stepsByID := make(map[string]int, len(plan.Steps))
	compiled := &compiledExecutionPlan{steps: make([]compiledExecutionStep, len(plan.Steps))}
	for index, definition := range plan.Steps {
		id, err := executionPlanID(fmt.Sprintf("execution plan step %d id", index+1), definition.ID)
		if err != nil {
			return nil, err
		}
		if _, exists := stepsByID[id]; exists {
			return nil, fmt.Errorf("execution plan step id %q is duplicated", id)
		}
		stepsByID[id] = index
		compiled.steps[index] = compiledExecutionStep{
			id:            id,
			scenarioIndex: -1,
			next:          -1,
			join:          -1,
			body:          -1,
			exit:          -1,
		}
	}

	entryID, err := executionPlanID("execution plan entry step id", plan.EntryStepID)
	if err != nil {
		return nil, err
	}
	entry, exists := stepsByID[entryID]
	if !exists {
		return nil, fmt.Errorf("execution plan entry step %q does not exist", entryID)
	}
	compiled.entry = entry

	usedScenarioSteps := make(map[int]struct{}, len(scenarioSteps))
	routeMetricIndex := 0
	for index, definition := range plan.Steps {
		next := &compiled.steps[index]
		switch definition.Kind {
		case ExecutionStepScenario:
			scenarioIndex, exists := scenarioByID[next.id]
			if !exists {
				return nil, fmt.Errorf("execution plan step %q does not reference a scenario step", next.id)
			}
			if _, duplicate := usedScenarioSteps[scenarioIndex]; duplicate {
				return nil, fmt.Errorf("scenario step %q appears more than once in the execution plan", next.id)
			}
			usedScenarioSteps[scenarioIndex] = struct{}{}
			next.kind = compiledExecutionScenario
			next.scenarioIndex = scenarioIndex
			next.next, err = resolveOptionalExecutionTarget(definition.NextStepID, stepsByID)
		case ExecutionStepBranch:
			next.kind = compiledExecutionBranch
			if len(definition.Routes) < 2 || len(definition.Routes) > MaxBranchRoutes {
				return nil, fmt.Errorf("branch %q must have between 2 and %d routes", next.id, MaxBranchRoutes)
			}
			next.join, err = resolveRequiredExecutionTarget("branch join", definition.JoinStepID, stepsByID)
			if err == nil && plan.Steps[next.join].Kind != ExecutionStepJoin {
				err = fmt.Errorf("branch %q join step %q must have kind %q", next.id, definition.JoinStepID, ExecutionStepJoin)
			}
			routeIDs := make(map[string]struct{}, len(definition.Routes))
			var totalWeight uint64
			for routeIndex, route := range definition.Routes {
				routeID, routeErr := executionPlanID(
					fmt.Sprintf("branch %q route %d id", next.id, routeIndex+1),
					route.ID,
				)
				if routeErr != nil {
					return nil, routeErr
				}
				if _, duplicate := routeIDs[routeID]; duplicate {
					return nil, fmt.Errorf("branch %q route id %q is duplicated", next.id, routeID)
				}
				routeIDs[routeID] = struct{}{}
				if route.Weight < 1 || route.Weight > maxBranchRouteWeight {
					return nil, fmt.Errorf("branch %q route %q weight must be between 1 and %d", next.id, routeID, maxBranchRouteWeight)
				}
				target, routeErr := resolveRequiredExecutionTarget("branch route target", route.TargetStepID, stepsByID)
				if routeErr != nil {
					return nil, routeErr
				}
				name := strings.TrimSpace(route.Name)
				if name == "" {
					name = routeID
				}
				if err := validateScenarioStepText("branch route name", name, MaxScenarioStepNameBytes); err != nil {
					return nil, err
				}
				totalWeight += uint64(route.Weight)
				next.routes = append(next.routes, compiledExecutionRoute{
					id: routeID, name: name, target: target, weight: uint64(route.Weight),
					metricsIndex: routeMetricIndex, scopeKey: next.id + ":" + routeID,
				})
				routeMetricIndex++
			}
			if totalWeight == 0 {
				return nil, fmt.Errorf("branch %q route weights must have a positive total", next.id)
			}
			next.routeWeight = totalWeight
		case ExecutionStepJoin:
			next.kind = compiledExecutionJoin
			next.next, err = resolveOptionalExecutionTarget(definition.NextStepID, stepsByID)
		case ExecutionStepLoop:
			next.kind = compiledExecutionLoop
			next.body, err = resolveRequiredExecutionTarget("loop body", definition.BodyStepID, stepsByID)
			if err == nil {
				next.exit, err = resolveOptionalExecutionTarget(definition.ExitStepID, stepsByID)
			}
			if definition.MaxIterations < 1 || definition.MaxIterations > MaxLoopIterations {
				return nil, fmt.Errorf("loop %q max iterations must be between 1 and %d", next.id, MaxLoopIterations)
			}
			next.maxIterations = definition.MaxIterations
		default:
			return nil, fmt.Errorf("execution plan step %q has unsupported kind %q", next.id, definition.Kind)
		}
		if err != nil {
			return nil, fmt.Errorf("execution plan step %q: %w", next.id, err)
		}
	}
	compiled.routeCount = routeMetricIndex
	if len(usedScenarioSteps) != len(scenarioSteps) {
		return nil, errors.New("every scenario step must appear exactly once in the execution plan")
	}
	entryScenario := &compiled.steps[compiled.entry]
	if entryScenario.kind != compiledExecutionScenario || scenarioSteps[entryScenario.scenarioIndex].kind != compiledRequest {
		return nil, errors.New("execution plan must start with a request step")
	}

	adjacency := executionAdjacency(compiled.steps, false)
	predecessors := executionPredecessors(adjacency)
	if err := validateExecutionReachability(compiled.entry, adjacency); err != nil {
		return nil, err
	}
	loopBodies, acyclicAdjacency, err := validateBoundedExecutionLoops(compiled.steps)
	if err != nil {
		return nil, err
	}
	if err := validateStructuredBranches(compiled.steps, acyclicAdjacency); err != nil {
		return nil, err
	}

	dominators := executionDominators(compiled.entry, predecessors)
	if err := attachPlannedAssertions(compiled.steps, scenarioSteps, plan.Steps, stepsByID, dominators); err != nil {
		return nil, err
	}
	if err := validatePlannedTemplates(compiled.steps, scenarioSteps, runtimeVariables, dominators); err != nil {
		return nil, err
	}

	maximum := uint64(len(compiled.steps))
	for loopIndex, bodySize := range loopBodies {
		iterations := uint64(compiled.steps[loopIndex].maxIterations)
		maximum += iterations * uint64(bodySize+1)
	}
	if maximum > MaxExecutionTransitionsPerIteration {
		return nil, fmt.Errorf(
			"execution plan may require %d transitions per iteration; maximum is %d",
			maximum,
			MaxExecutionTransitionsPerIteration,
		)
	}
	compiled.maxTransitions = int(maximum)
	return compiled, nil
}

func executionPlanID(label string, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	if trimmed != value {
		return "", fmt.Errorf("%s must not start or end with whitespace", label)
	}
	if err := validateScenarioStepText(label, trimmed, MaxScenarioStepIDBytes); err != nil {
		return "", err
	}
	return trimmed, nil
}

func resolveRequiredExecutionTarget(label string, id string, stepsByID map[string]int) (int, error) {
	trimmed, err := executionPlanID(label, id)
	if err != nil {
		return -1, err
	}
	target, exists := stepsByID[trimmed]
	if !exists {
		return -1, fmt.Errorf("%s %q does not exist", label, trimmed)
	}
	return target, nil
}

func resolveOptionalExecutionTarget(id string, stepsByID map[string]int) (int, error) {
	if id == "" {
		return -1, nil
	}
	return resolveRequiredExecutionTarget("next step", id, stepsByID)
}

func executionAdjacency(steps []compiledExecutionStep, removeLoopBodies bool) [][]int {
	adjacency := make([][]int, len(steps))
	for index := range steps {
		step := &steps[index]
		switch step.kind {
		case compiledExecutionScenario, compiledExecutionJoin:
			if step.next >= 0 {
				adjacency[index] = append(adjacency[index], step.next)
			}
		case compiledExecutionBranch:
			for _, route := range step.routes {
				adjacency[index] = append(adjacency[index], route.target)
			}
		case compiledExecutionLoop:
			if !removeLoopBodies {
				adjacency[index] = append(adjacency[index], step.body)
			}
			if step.exit >= 0 {
				adjacency[index] = append(adjacency[index], step.exit)
			}
		}
	}
	return adjacency
}

func executionPredecessors(adjacency [][]int) [][]int {
	predecessors := make([][]int, len(adjacency))
	for source, targets := range adjacency {
		for _, target := range targets {
			predecessors[target] = append(predecessors[target], source)
		}
	}
	return predecessors
}

func validateExecutionReachability(entry int, adjacency [][]int) error {
	visited := make([]bool, len(adjacency))
	queue := []int{entry}
	visited[entry] = true
	for cursor := 0; cursor < len(queue); cursor++ {
		for _, target := range adjacency[queue[cursor]] {
			if !visited[target] {
				visited[target] = true
				queue = append(queue, target)
			}
		}
	}
	for index, reachable := range visited {
		if !reachable {
			return fmt.Errorf("execution plan step %d is unreachable from the entry step", index+1)
		}
	}
	return nil
}

func validateBoundedExecutionLoops(
	steps []compiledExecutionStep,
) (map[int]int, [][]int, error) {
	acyclic := executionAdjacency(steps, true)
	if cycle := executionCycleNode(acyclic); cycle >= 0 {
		return nil, nil, fmt.Errorf("execution plan contains an unbounded cycle at step %q", steps[cycle].id)
	}
	loopBodies := make(map[int]int)
	reverse := executionPredecessors(acyclic)
	for loopIndex := range steps {
		loop := &steps[loopIndex]
		if loop.kind != compiledExecutionLoop {
			continue
		}
		canReachLoop := reverseReachable(loopIndex, reverse)
		if !canReachLoop[loop.body] {
			return nil, nil, fmt.Errorf("loop %q body does not return to the loop", loop.id)
		}
		bodyRegion := forwardUntil(loop.body, loopIndex, acyclic)
		for node := range bodyRegion {
			if !canReachLoop[node] {
				return nil, nil, fmt.Errorf("loop %q body can escape without returning to the loop", loop.id)
			}
			if node != loopIndex && steps[node].kind == compiledExecutionLoop {
				return nil, nil, fmt.Errorf("loop %q cannot contain nested loop %q", loop.id, steps[node].id)
			}
		}
		if loop.exit >= 0 {
			if _, inside := bodyRegion[loop.exit]; inside || loop.exit == loopIndex {
				return nil, nil, fmt.Errorf("loop %q exit must be outside its body", loop.id)
			}
		}
		loopBodies[loopIndex] = len(bodyRegion)
	}
	return loopBodies, acyclic, nil
}

func executionCycleNode(adjacency [][]int) int {
	incoming := make([]int, len(adjacency))
	for _, targets := range adjacency {
		for _, target := range targets {
			incoming[target]++
		}
	}
	queue := make([]int, 0, len(adjacency))
	for index, count := range incoming {
		if count == 0 {
			queue = append(queue, index)
		}
	}
	for cursor := 0; cursor < len(queue); cursor++ {
		for _, target := range adjacency[queue[cursor]] {
			incoming[target]--
			if incoming[target] == 0 {
				queue = append(queue, target)
			}
		}
	}
	for index, count := range incoming {
		if count > 0 {
			return index
		}
	}
	return -1
}

func reverseReachable(target int, reverse [][]int) []bool {
	reachable := make([]bool, len(reverse))
	queue := []int{target}
	reachable[target] = true
	for cursor := 0; cursor < len(queue); cursor++ {
		for _, predecessor := range reverse[queue[cursor]] {
			if !reachable[predecessor] {
				reachable[predecessor] = true
				queue = append(queue, predecessor)
			}
		}
	}
	return reachable
}

func forwardUntil(entry int, stop int, adjacency [][]int) map[int]struct{} {
	visited := make(map[int]struct{})
	queue := []int{entry}
	for cursor := 0; cursor < len(queue); cursor++ {
		current := queue[cursor]
		if current == stop {
			continue
		}
		if _, exists := visited[current]; exists {
			continue
		}
		visited[current] = struct{}{}
		queue = append(queue, adjacency[current]...)
	}
	return visited
}

func validateStructuredBranches(steps []compiledExecutionStep, adjacency [][]int) error {
	joinOwners := make(map[int]int)
	reverse := executionPredecessors(adjacency)
	for branchIndex := range steps {
		branch := &steps[branchIndex]
		if branch.kind != compiledExecutionBranch {
			continue
		}
		if owner, exists := joinOwners[branch.join]; exists {
			return fmt.Errorf("branches %q and %q cannot share join %q", steps[owner].id, branch.id, steps[branch.join].id)
		}
		joinOwners[branch.join] = branchIndex
		canReachJoin := reverseReachable(branch.join, reverse)
		regions := make([]map[int]struct{}, len(branch.routes))
		for routeIndex, route := range branch.routes {
			if !canReachJoin[route.target] {
				return fmt.Errorf("branch %q route %q does not reach join %q", branch.id, route.id, steps[branch.join].id)
			}
			region := forwardUntil(route.target, branch.join, adjacency)
			for node := range region {
				if !canReachJoin[node] {
					return fmt.Errorf("branch %q route %q can bypass join %q", branch.id, route.id, steps[branch.join].id)
				}
			}
			regions[routeIndex] = region
		}
		for left := 0; left < len(regions); left++ {
			for right := left + 1; right < len(regions); right++ {
				for node := range regions[left] {
					if _, overlaps := regions[right][node]; overlaps {
						return fmt.Errorf(
							"branch %q routes %q and %q merge before join %q at step %q",
							branch.id,
							branch.routes[left].id,
							branch.routes[right].id,
							steps[branch.join].id,
							steps[node].id,
						)
					}
				}
			}
		}
		for _, region := range regions {
			for node := range region {
				if steps[node].kind == compiledExecutionBranch {
					if _, nestedJoinInside := region[steps[node].join]; !nestedJoinInside {
						return fmt.Errorf(
							"branch %q nested branch %q must join before %q",
							branch.id,
							steps[node].id,
							steps[branch.join].id,
						)
					}
				}
			}
		}
	}
	for index := range steps {
		if steps[index].kind == compiledExecutionJoin {
			if _, owned := joinOwners[index]; !owned {
				return fmt.Errorf("join %q is not assigned to a branch", steps[index].id)
			}
		}
	}
	return nil
}

func executionDominators(entry int, predecessors [][]int) [][]uint64 {
	words := (len(predecessors) + 63) / 64
	dominators := make([][]uint64, len(predecessors))
	for index := range dominators {
		dominators[index] = make([]uint64, words)
		if index == entry {
			setExecutionBit(dominators[index], index)
			continue
		}
		for word := range dominators[index] {
			dominators[index][word] = ^uint64(0)
		}
	}
	changed := true
	for changed {
		changed = false
		for node := range predecessors {
			if node == entry || len(predecessors[node]) == 0 {
				continue
			}
			next := append([]uint64(nil), dominators[predecessors[node][0]]...)
			for _, predecessor := range predecessors[node][1:] {
				for word := range next {
					next[word] &= dominators[predecessor][word]
				}
			}
			setExecutionBit(next, node)
			if !equalExecutionBits(next, dominators[node]) {
				dominators[node] = next
				changed = true
			}
		}
	}
	return dominators
}

func setExecutionBit(bits []uint64, index int) {
	bits[index/64] |= uint64(1) << (index % 64)
}

func hasExecutionBit(bits []uint64, index int) bool {
	return bits[index/64]&(uint64(1)<<(index%64)) != 0
}

func equalExecutionBits(left []uint64, right []uint64) bool {
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func attachPlannedAssertions(
	steps []compiledExecutionStep,
	scenarioSteps []compiledStep,
	definitions []ExecutionPlanStep,
	stepsByID map[string]int,
	dominators [][]uint64,
) error {
	for planIndex := range steps {
		instruction := &steps[planIndex]
		if instruction.kind != compiledExecutionScenario {
			continue
		}
		assertionStep := &scenarioSteps[instruction.scenarioIndex]
		if assertionStep.kind != compiledAssert {
			continue
		}
		requestID := definitions[planIndex].RequestStepID
		requestPlanIndex, err := resolveRequiredExecutionTarget("assertion request step", requestID, stepsByID)
		if err != nil {
			return fmt.Errorf("assertion %q: %w", instruction.id, err)
		}
		requestInstruction := &steps[requestPlanIndex]
		if requestInstruction.kind != compiledExecutionScenario ||
			scenarioSteps[requestInstruction.scenarioIndex].kind != compiledRequest {
			return fmt.Errorf("assertion %q request step %q is not a request", instruction.id, requestID)
		}
		if requestPlanIndex == planIndex || !hasExecutionBit(dominators[planIndex], requestPlanIndex) {
			return fmt.Errorf("assertion %q request step %q does not run on every path to the assertion", instruction.id, requestID)
		}
		request := &scenarioSteps[requestInstruction.scenarioIndex].request
		assertionStep.assertion.requestMetricsIndex = request.metricsIndex
		request.assertions = append(request.assertions, assertionStep.assertion)
	}
	return nil
}

func validatePlannedTemplates(
	steps []compiledExecutionStep,
	scenarioSteps []compiledStep,
	runtimeVariables map[string]string,
	dominators [][]uint64,
) error {
	planIndexByScenario := make(map[int]int, len(scenarioSteps))
	producers := make(map[string][]int)
	for planIndex := range steps {
		instruction := &steps[planIndex]
		if instruction.kind != compiledExecutionScenario {
			continue
		}
		planIndexByScenario[instruction.scenarioIndex] = planIndex
		step := &scenarioSteps[instruction.scenarioIndex]
		if step.kind == compiledRequest {
			for _, capture := range step.request.captures {
				producers[capture.name] = append(producers[capture.name], planIndex)
			}
		}
	}
	for scenarioIndex := range scenarioSteps {
		step := &scenarioSteps[scenarioIndex]
		if step.kind != compiledRequest {
			continue
		}
		planIndex := planIndexByScenario[scenarioIndex]
		for _, name := range step.request.templateNames {
			if _, available := runtimeVariables[name]; available {
				continue
			}
			available := false
			for _, producer := range producers[name] {
				if producer != planIndex && hasExecutionBit(dominators[planIndex], producer) {
					available = true
					break
				}
			}
			if !available {
				return fmt.Errorf(
					"scenario step %q template variable %q is not defined on every path by an earlier capture",
					step.id,
					name,
				)
			}
		}
	}
	return nil
}

func branchRouteDescriptors(plan *compiledExecutionPlan) []branchRouteDescriptor {
	if plan == nil || plan.routeCount == 0 {
		return nil
	}
	descriptors := make([]branchRouteDescriptor, plan.routeCount)
	for stepIndex := range plan.steps {
		step := &plan.steps[stepIndex]
		if step.kind != compiledExecutionBranch {
			continue
		}
		for _, route := range step.routes {
			descriptors[route.metricsIndex] = branchRouteDescriptor{
				branchID: step.id,
				routeID:  route.id,
				name:     route.name,
			}
		}
	}
	return descriptors
}
