package studio

import (
	"fmt"
	"sort"
)

type stepEvaluator func(inputs []float64, t float64) float64

type executionSegment struct {
	depth         int
	blockIDs      []int64
	inputSignals  []string
	outputSignals []string
}

type executionStep struct {
	depth   int
	blockID int64
}

type executionPartition struct {
	segments []executionSegment
	steps    []executionStep
}

func buildExecutionPartition(
	blocks []Block,
	connections []Connection,
	isStep func(Block) bool,
) (executionPartition, error) {
	if len(blocks) == 0 {
		return executionPartition{}, nil
	}

	ordered := append([]Block(nil), blocks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	indexByID := make(map[int64]int, len(ordered))
	for index, block := range ordered {
		indexByID[block.ID] = index
	}
	edges := make([][]int, len(ordered))
	selfLoop := make([]bool, len(ordered))
	for _, connection := range connections {
		from, fromOK := indexByID[connection.SourceID]
		to, toOK := indexByID[connection.TargetID]
		if !fromOK || !toOK {
			return executionPartition{}, invalid("execution partition references a missing block")
		}
		edges[from] = append(edges[from], to)
		if from == to {
			selfLoop[from] = true
		}
	}
	for i := range edges {
		sort.Ints(edges[i])
	}

	components, componentOf := stronglyConnectedComponents(edges)
	componentStep := make([]bool, len(components))
	for componentIndex, members := range components {
		for _, member := range members {
			if isStep(ordered[member]) {
				componentStep[componentIndex] = true
			}
		}
		if componentStep[componentIndex] &&
			(len(members) > 1 || selfLoop[members[0]]) {
			return executionPartition{}, invalid(
				"sample-stepped block %s participates in a feedback cycle; keep feedback inside one LTI segment",
				ordered[members[0]].Name,
			)
		}
	}

	componentEdges := make([]map[int]struct{}, len(components))
	indegree := make([]int, len(components))
	for i := range componentEdges {
		componentEdges[i] = make(map[int]struct{})
	}
	for from, tos := range edges {
		fromComponent := componentOf[from]
		for _, to := range tos {
			toComponent := componentOf[to]
			if fromComponent == toComponent {
				continue
			}
			if _, exists := componentEdges[fromComponent][toComponent]; exists {
				continue
			}
			componentEdges[fromComponent][toComponent] = struct{}{}
			indegree[toComponent]++
		}
	}

	depth := make([]int, len(components))
	queue := make([]int, 0, len(components))
	for componentIndex, degree := range indegree {
		if degree == 0 {
			queue = append(queue, componentIndex)
		}
	}
	sort.Ints(queue)
	visited := 0
	for len(queue) > 0 {
		component := queue[0]
		queue = queue[1:]
		visited++
		targets := sortedSetKeys(componentEdges[component])
		for _, target := range targets {
			candidate := depth[component]
			if componentStep[component] {
				candidate++
			}
			if candidate > depth[target] {
				depth[target] = candidate
			}
			indegree[target]--
			if indegree[target] == 0 {
				queue = append(queue, target)
				sort.Ints(queue)
			}
		}
	}
	if visited != len(components) {
		return executionPartition{}, fmt.Errorf("condensed execution graph is cyclic")
	}

	segmentByDepth := make(map[int]*executionSegment)
	blockSegment := make(map[int64]*executionSegment)
	var steps []executionStep
	for componentIndex, members := range components {
		if componentStep[componentIndex] {
			steps = append(steps, executionStep{
				depth: depth[componentIndex], blockID: ordered[members[0]].ID,
			})
			continue
		}
		segment := segmentByDepth[depth[componentIndex]]
		if segment == nil {
			segment = &executionSegment{depth: depth[componentIndex]}
			segmentByDepth[depth[componentIndex]] = segment
		}
		for _, member := range members {
			blockID := ordered[member].ID
			segment.blockIDs = append(segment.blockIDs, blockID)
			blockSegment[blockID] = segment
		}
	}

	for _, segment := range segmentByDepth {
		sort.Slice(segment.blockIDs, func(i, j int) bool {
			return segment.blockIDs[i] < segment.blockIDs[j]
		})
		for _, blockID := range segment.blockIDs {
			block := ordered[indexByID[blockID]]
			if block.Kind.isSource() {
				segment.inputSignals = append(segment.inputSignals, sourceSignalName(blockID))
			}
			if block.Kind.isSink() {
				segment.outputSignals = append(segment.outputSignals, outputSignalName(blockID, 0))
			}
		}
	}

	for _, connection := range connections {
		fromSegment := blockSegment[connection.SourceID]
		toSegment := blockSegment[connection.TargetID]
		if fromSegment == toSegment {
			continue
		}
		if toSegment != nil {
			toSegment.inputSignals = append(
				toSegment.inputSignals,
				inputSignalName(connection.TargetID, connection.TargetPort),
			)
		}
		if fromSegment != nil {
			fromSegment.outputSignals = append(
				fromSegment.outputSignals,
				outputSignalName(connection.SourceID, connection.SourcePort),
			)
		}
	}

	segments := make([]executionSegment, 0, len(segmentByDepth))
	for _, segment := range segmentByDepth {
		segment.inputSignals = sortedUniqueStrings(segment.inputSignals)
		segment.outputSignals = sortedUniqueStrings(segment.outputSignals)
		segments = append(segments, *segment)
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].depth < segments[j].depth })
	sort.Slice(steps, func(i, j int) bool {
		if steps[i].depth != steps[j].depth {
			return steps[i].depth < steps[j].depth
		}
		return steps[i].blockID < steps[j].blockID
	})
	return executionPartition{segments: segments, steps: steps}, nil
}

func stronglyConnectedComponents(edges [][]int) ([][]int, []int) {
	index := 0
	indices := make([]int, len(edges))
	lowlink := make([]int, len(edges))
	onStack := make([]bool, len(edges))
	for i := range indices {
		indices[i] = -1
	}
	var stack []int
	var components [][]int
	var visit func(int)
	visit = func(node int) {
		indices[node] = index
		lowlink[node] = index
		index++
		stack = append(stack, node)
		onStack[node] = true

		for _, next := range edges[node] {
			switch {
			case indices[next] == -1:
				visit(next)
				lowlink[node] = min(lowlink[node], lowlink[next])
			case onStack[next]:
				lowlink[node] = min(lowlink[node], indices[next])
			}
		}
		if lowlink[node] != indices[node] {
			return
		}
		var component []int
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			component = append(component, member)
			if member == node {
				break
			}
		}
		sort.Ints(component)
		components = append(components, component)
	}
	for node := range edges {
		if indices[node] == -1 {
			visit(node)
		}
	}
	componentOf := make([]int, len(edges))
	for componentIndex, members := range components {
		for _, member := range members {
			componentOf[member] = componentIndex
		}
	}
	return components, componentOf
}

func sortedSetKeys(values map[int]struct{}) []int {
	keys := make([]int, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Ints(keys)
	return keys
}

func sortedUniqueStrings(values []string) []string {
	sort.Strings(values)
	if len(values) < 2 {
		return values
	}
	unique := values[:1]
	for _, value := range values[1:] {
		if value != unique[len(unique)-1] {
			unique = append(unique, value)
		}
	}
	return unique
}
