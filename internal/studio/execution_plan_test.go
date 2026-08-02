package studio

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestExecutionPartitionKeepsLTIFeedbackInsideOneSegment(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Setpoint"},
		{ID: 2, Kind: BlockSum, Name: "Error"},
		{ID: 3, Kind: BlockGain, Name: "Controller"},
		{ID: 4, Kind: BlockLag, Name: "Plant"},
		{ID: 5, Kind: BlockScope, Name: "Output"},
	}
	partition, err := buildExecutionPartition(blocks, []Connection{
		{SourceID: 1, TargetID: 2, TargetPort: 0},
		{SourceID: 2, TargetID: 3},
		{SourceID: 3, TargetID: 4},
		{SourceID: 4, TargetID: 2, TargetPort: 1},
		{SourceID: 4, TargetID: 5},
	}, func(Block) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if len(partition.segments) != 1 || len(partition.steps) != 0 {
		t.Fatalf("partition = %#v, want one LTI segment", partition)
	}
	if got, want := partition.segments[0].blockIDs, []int64{1, 2, 3, 4, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("feedback segment blocks = %v, want %v", got, want)
	}
}

func TestExecutionPartitionIncludesEverySumBranchBoundary(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Step"},
		{ID: 2, Kind: BlockGain, Name: "Gain"},
		{ID: 3, Kind: BlockGain, Name: "Synthetic saturation"},
		{ID: 4, Kind: BlockSum, Name: "Sum", Parameters: Parameters{Signs: "++"}},
		{ID: 5, Kind: BlockScope, Name: "Scope"},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 2},
		{ID: 2, SourceID: 2, TargetID: 3},
		{ID: 3, SourceID: 2, TargetID: 4, TargetPort: 0},
		{ID: 4, SourceID: 3, TargetID: 4, TargetPort: 1},
		{ID: 5, SourceID: 4, TargetID: 5},
	}
	partition, err := buildExecutionPartition(
		blocks,
		connections,
		func(block Block) bool { return block.ID == 3 },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(partition.segments) != 2 || len(partition.steps) != 1 {
		t.Fatalf("partition = %#v, want two segments and one step", partition)
	}
	if got, want := partition.segments[0].blockIDs, []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("depth-0 blocks = %v, want %v", got, want)
	}
	if got, want := partition.segments[0].outputSignals, []string{"block_2_output_0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("depth-0 outputs = %v, want %v", got, want)
	}
	if got, want := partition.segments[1].blockIDs, []int64{4, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("depth-1 blocks = %v, want %v", got, want)
	}
	if got, want := partition.segments[1].inputSignals, []string{
		"block_4_input_0", "block_4_input_1",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("depth-1 inputs = %v, want %v", got, want)
	}
	if partition.steps[0] != (executionStep{depth: 0, blockID: 3}) {
		t.Fatalf("step = %#v", partition.steps[0])
	}

	reversed := append([]Connection(nil), connections...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	again, err := buildExecutionPartition(
		blocks,
		reversed,
		func(block Block) bool { return block.ID == 3 },
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(partition, again) {
		t.Fatalf("connection order changed partition:\n%#v\n%#v", partition, again)
	}
}

func TestExecutionPartitionRefusesCycleAcrossStepBoundary(t *testing.T) {
	_, err := buildExecutionPartition([]Block{
		{ID: 1, Kind: BlockGain, Name: "Linear"},
		{ID: 2, Kind: BlockGain, Name: "Synthetic step"},
	}, []Connection{
		{SourceID: 1, TargetID: 2},
		{SourceID: 2, TargetID: 1},
	}, func(block Block) bool { return block.ID == 2 })
	if err == nil || !strings.Contains(err.Error(), "participates in a feedback cycle") {
		t.Fatalf("error = %v, want step-cycle refusal", err)
	}
}

func TestExecutionPartitionIsAcyclicForEveryDAGThroughFiveBlocks(t *testing.T) {
	for count := 1; count <= 5; count++ {
		blocks := make([]Block, count)
		for i := range blocks {
			blocks[i] = Block{ID: int64(i + 1), Kind: BlockGain, Name: "node"}
		}
		var possible [][2]int
		for from := 0; from < count; from++ {
			for to := from + 1; to < count; to++ {
				possible = append(possible, [2]int{from, to})
			}
		}
		for edgeMask := 0; edgeMask < 1<<len(possible); edgeMask++ {
			var connections []Connection
			for edgeIndex, edge := range possible {
				if edgeMask&(1<<edgeIndex) != 0 {
					connections = append(connections, Connection{
						SourceID: int64(edge[0] + 1),
						TargetID: int64(edge[1] + 1),
					})
				}
			}
			for stepMask := 0; stepMask < 1<<count; stepMask++ {
				_, err := buildExecutionPartition(
					blocks,
					connections,
					func(block Block) bool {
						return stepMask&(1<<int(block.ID-1)) != 0
					},
				)
				if err != nil {
					t.Fatalf(
						"count=%d edgeMask=%b stepMask=%b: %v",
						count, edgeMask, stepMask, err,
					)
				}
			}
		}
	}
}

func TestPerSampleControlsysDriverMatchesBatchSimulation(t *testing.T) {
	continuous, err := controlsys.New(
		mat.NewDense(1, 1, []float64{-2}),
		mat.NewDense(1, 1, []float64{2}),
		mat.NewDense(1, 1, []float64{1}),
		mat.NewDense(1, 1, nil),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	discrete, err := continuous.DiscretizeZOH(0.1)
	if err != nil {
		t.Fatal(err)
	}
	input := mat.NewDense(1, 51, nil)
	for sample := range 51 {
		input.Set(0, sample, float64(sample%7)-2)
	}
	batch, err := discrete.Simulate(input, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stepped, err := simulateSystemByStep(discrete, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	for sample := range 51 {
		if got, want := stepped.At(0, sample), batch.Y.At(0, sample); got != want {
			t.Fatalf("sample %d = %.17g, batch %.17g", sample, got, want)
		}
	}
}

func TestSimulationRecordsExecutionFidelity(t *testing.T) {
	continuousRun, err := simulate([]Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockLag, Name: "Plant", Parameters: Parameters{TimeConstant: 1}},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}, []Connection{
		{SourceID: 1, TargetID: 2},
		{SourceID: 2, TargetID: 3},
	}, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if continuousRun.Fidelity.Driver != "batch-lsim" ||
		continuousRun.Fidelity.BaseStep != 0.1 ||
		continuousRun.Fidelity.ModelDomain != string(timeDomainContinuous) ||
		continuousRun.Fidelity.SourceHold != "piecewise-constant" ||
		continuousRun.Fidelity.SegmentCount != 1 {
		t.Fatalf("continuous fidelity = %#v", continuousRun.Fidelity)
	}

	delayRun, err := simulate([]Block{
		{ID: 1, Kind: BlockSine, Name: "Input", Parameters: Parameters{Amplitude: 1, Frequency: 1}},
		{ID: 2, Kind: BlockDelay, Name: "Delay", Parameters: Parameters{
			Delay: 0.2, DelayMode: delayModeExact,
		}},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}, []Connection{
		{SourceID: 1, TargetID: 2},
		{SourceID: 2, TargetID: 3},
	}, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if delayRun.Fidelity.Driver != "delay-aware-simulate" ||
		delayRun.Fidelity.SourceHold != "sampled-zero-order-hold" ||
		!delayRun.Fidelity.ExactDelayAligned ||
		!reflect.DeepEqual(delayRun.Fidelity.DelayModels, []string{delayModeExact}) ||
		!reflect.DeepEqual(delayRun.Fidelity.Delays, []DelayProvenance{{
			BlockID: 2, BlockName: "Delay", Representation: delayModeExact,
			Delay: 0.2, SampleTime: 0.1, Aligned: true,
		}}) {
		t.Fatalf("delay fidelity = %#v", delayRun.Fidelity)
	}
}

func TestSimulationRecordsDiscreteRatesAndDelayApproximations(t *testing.T) {
	tests := []struct {
		name       string
		block      Block
		wantDomain string
		wantRate   []BlockRate
		wantDelay  []DelayProvenance
	}{
		{
			name: "inherited unit delay",
			block: Block{ID: 2, Kind: BlockUnitDelay, Name: "Memory", Parameters: Parameters{
				SampleTimeMode: string(sampleTimeInherited),
			}},
			wantDomain: string(timeDomainDiscrete),
			wantRate: []BlockRate{{
				BlockID: 2, BlockName: "Memory", Mode: string(sampleTimeInherited),
				SampleTime: 0.1, UpdateEvery: 1,
			}},
		},
		{
			name: "pade",
			block: Block{ID: 2, Kind: BlockDelay, Name: "Pipe", Parameters: Parameters{
				Delay: 0.4, DelayMode: delayModePade, Approximation: 4,
			}},
			wantDomain: string(timeDomainContinuous),
			wantDelay: []DelayProvenance{{
				BlockID: 2, BlockName: "Pipe", Representation: delayModePade,
				Delay: 0.4, ApproximationOrder: 4,
			}},
		},
		{
			name: "thiran",
			block: Block{ID: 2, Kind: BlockDelay, Name: "Pipe", Parameters: Parameters{
				Delay: 0.35, DelayMode: delayModeThiran, Approximation: 3,
				SampleTime: 0.1, SampleTimeMode: string(sampleTimeExplicit),
			}},
			wantDomain: string(timeDomainDiscrete),
			wantRate: []BlockRate{{
				BlockID: 2, BlockName: "Pipe", Mode: string(sampleTimeExplicit),
				SampleTime: 0.1, UpdateEvery: 1,
			}},
			wantDelay: []DelayProvenance{{
				BlockID: 2, BlockName: "Pipe", Representation: delayModeThiran,
				Delay: 0.35, ApproximationOrder: 3,
				SampleTime: 0.1, SampleTimeMode: string(sampleTimeExplicit),
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run, err := simulate([]Block{
				{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
				test.block,
				{ID: 3, Kind: BlockScope, Name: "Output"},
			}, []Connection{
				{SourceID: 1, TargetID: 2},
				{SourceID: 2, TargetID: 3},
			}, SimulationRequest{Duration: 1, SampleTime: 0.1})
			if err != nil {
				t.Fatal(err)
			}
			if run.Fidelity.ModelDomain != test.wantDomain ||
				!reflect.DeepEqual(run.Fidelity.BlockRates, test.wantRate) ||
				!reflect.DeepEqual(run.Fidelity.Delays, test.wantDelay) {
				t.Fatalf("fidelity = %#v", run.Fidelity)
			}
		})
	}
}
