package studio

import (
	"context"
	"math"
	"math/cmplx"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestRobustSynthesisUsesNamedPartitionsAndIndependentNormOracles(
	t *testing.T,
) {
	service, flowID, controllerID := robustSynthesisStudio(t)
	ctx := context.Background()
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []RobustSynthesisMethod{
		RobustSynthesisH2, RobustSynthesisHinf,
	} {
		t.Run(string(method), func(t *testing.T) {
			candidate, err := service.DesignRobustController(
				ctx, flowID, RobustSynthesisRequest{Method: method},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !equalStrings(candidate.Partitions.Exogenous, []string{"w1", "w2"}) ||
				!equalStrings(candidate.Partitions.Regulated, []string{"z1", "z2"}) ||
				!equalStrings(candidate.Partitions.Measurement, []string{"y"}) ||
				!equalStrings(candidate.Partitions.Control, []string{"u"}) {
				t.Fatalf("named partitions = %#v", candidate.Partitions)
			}
			if !candidate.Evidence.StableClosedLoop ||
				candidate.Evidence.AchievedNorm <= 0 ||
				candidate.X == nil || candidate.Y == nil {
				t.Fatalf("synthesis evidence = %#v", candidate.Evidence)
			}
			if !equalStrings(candidate.Controller.InputName, []string{"y"}) ||
				!equalStrings(candidate.Controller.OutputName, []string{"u"}) {
				t.Fatalf(
					"controller names = inputs %v outputs %v",
					candidate.Controller.InputName, candidate.Controller.OutputName,
				)
			}
			switch method {
			case RobustSynthesisH2:
				oracle := independentContinuousH2Norm(t, candidate.ClosedLoop)
				if math.Abs(candidate.Evidence.AchievedNorm-oracle) >
					1e-9*math.Max(1, oracle) {
					t.Fatalf(
						"H2 norm = %.12g, independent Lyapunov oracle %.12g",
						candidate.Evidence.AchievedNorm, oracle,
					)
				}
			case RobustSynthesisHinf:
				densePeak := independentMIMODensePeak(t, candidate.ClosedLoop)
				if densePeak > candidate.Evidence.GammaBound*1.01 {
					t.Fatalf(
						"dense Hinf peak %.9g exceeds gamma %.9g",
						densePeak, candidate.Evidence.GammaBound,
					)
				}
				if candidate.Evidence.AchievedNorm >
					candidate.Evidence.GammaBound &&
					len(candidate.Warnings) == 0 {
					t.Fatal("gamma excess was not disclosed")
				}
				if densePeak > candidate.Evidence.AchievedNorm*(1+1e-8) ||
					candidate.Evidence.AchievedNorm-densePeak >
						0.03*math.Max(1, candidate.Evidence.AchievedNorm) {
					t.Fatalf(
						"Hinf norm %.9g, dense oracle %.9g",
						candidate.Evidence.AchievedNorm, densePeak,
					)
				}
			}
			after, err := service.Snapshot(ctx, flowID)
			if err != nil {
				t.Fatal(err)
			}
			if !after.Flow.ModelUpdatedAt.Equal(before.Flow.ModelUpdatedAt) ||
				!reflect.DeepEqual(
					findBlock(t, after.Blocks, controllerID).Parameters,
					findBlock(t, before.Blocks, controllerID).Parameters,
				) {
				t.Fatal("robust synthesis candidate mutated the model")
			}
		})
	}
}

func TestRobustSynthesisReviewApplyAndUndo(t *testing.T) {
	service, flowID, controllerID := robustSynthesisStudio(t)
	ctx := context.Background()
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := service.DesignRobustController(
		ctx, flowID, RobustSynthesisRequest{Method: RobustSynthesisH2},
	)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewRobustSynthesisCandidate(ctx, candidate, 5)
	if err != nil {
		t.Fatal(err)
	}
	if review.Kind != "robust-synthesis" || review.Algorithm != "h2" ||
		!review.ApplyAvailable || review.Robustness.Candidate == nil {
		t.Fatalf("robust review = %#v", review)
	}
	applied, err := service.ApplyRobustSynthesisCandidateWithUndo(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(
		findBlock(t, applied.Snapshot.Blocks, controllerID).Parameters,
		findBlock(t, before.Blocks, controllerID).Parameters,
	) {
		t.Fatal("robust synthesis apply did not replace the controller")
	}
	restored, err := service.UndoControllerCandidate(ctx, applied.Undo)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		findBlock(t, restored.Blocks, controllerID).Parameters,
		findBlock(t, before.Blocks, controllerID).Parameters,
	) {
		t.Fatal("robust synthesis undo did not restore the controller")
	}
}

func TestRobustSynthesisPreflightRefusesUnsupportedPlantSemantics(t *testing.T) {
	base, err := controlsys.New(
		mat.NewDense(1, 1, []float64{-1}),
		mat.NewDense(1, 2, []float64{1, 1}),
		mat.NewDense(2, 1, []float64{1, 1}),
		mat.NewDense(2, 2, []float64{0, 1, 1, 0}),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	delayed := base.Copy()
	delayed.InputDelay = []float64{0.2, 0}
	if err := validateRobustGeneralizedPlant(delayed); err == nil ||
		!strings.Contains(err.Error(), "refuses delayed") {
		t.Fatalf("delay preflight error = %v", err)
	}
	discrete, err := base.DiscretizeZOH(0.1)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRobustGeneralizedPlant(discrete); err == nil ||
		!strings.Contains(err.Error(), "continuous") {
		t.Fatalf("discrete preflight error = %v", err)
	}
	descriptor := base.Copy()
	descriptor.E = mat.NewDense(1, 1, []float64{2})
	if err := validateRobustGeneralizedPlant(descriptor); err == nil ||
		!strings.Contains(err.Error(), "descriptor") {
		t.Fatalf("descriptor preflight error = %v", err)
	}
}

func TestRobustSynthesisContainsDependencyPanics(t *testing.T) {
	_, err := containSynthesisPanic(
		"test synthesis",
		func() (*controlsys.HinfSynResult, error) {
			panic("numerical backend failure")
		},
	)
	if err == nil || !strings.Contains(
		err.Error(), "test synthesis panicked: numerical backend failure",
	) {
		t.Fatalf("contained synthesis error = %v", err)
	}
}

func robustSynthesisStudio(t *testing.T) (*Studio, int64, int64) {
	t.Helper()
	ctx := context.Background()
	service := openTestStudio(
		t, filepath.Join(t.TempDir(), "robust-synthesis.db"),
	)
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFlow(
		ctx, current.Project.ID, "Named robust synthesis",
	)
	if err != nil {
		t.Fatal(err)
	}
	flowID := created.Snapshot.Flow.ID
	_, muxID, err := service.AddBlock(
		ctx, flowID, BlockMux, Point{X: 100, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, plantID, err := service.AddBlock(
		ctx, flowID, BlockStateSpace, Point{X: 300, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, demuxID, err := service.AddBlock(
		ctx, flowID, BlockDemux, Point{X: 500, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, controllerID, err := service.AddBlock(
		ctx, flowID, BlockStateSpace, Point{X: 500, Y: 300},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateBlock(ctx, muxID, BlockUpdate{
		Name:       "Generalized inputs",
		Parameters: map[string]string{"output_names": "w1, w2, u"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateBlock(ctx, plantID, BlockUpdate{
		Name: "Generalized plant",
		Parameters: map[string]string{
			"a":           "0 1; -2 -3",
			"b":           "1 0 0; 0 1 1",
			"c":           "1 0; 0 0; 1 0",
			"d":           "0 0 0; 0 0 1; 0.1 0.1 0",
			"input_names": "w1, w2, u", "output_names": "z1, z2, y",
			"state_names": "x1, x2", "time_domain": "continuous",
			"sample_time": "0.1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateBlock(ctx, demuxID, BlockUpdate{
		Name:       "Generalized outputs",
		Parameters: map[string]string{"input_names": "z1, z2, y"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateBlock(ctx, controllerID, BlockUpdate{
		Name: "Robust controller",
		Parameters: map[string]string{
			"a": "-2", "b": "1", "c": "0", "d": "0",
			"input_names": "y", "output_names": "u",
			"state_names": "controller.x", "time_domain": "continuous",
			"sample_time": "0.1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, wire := range []Wire{
		{SourceID: controllerID, TargetID: muxID, TargetPort: 2},
		{SourceID: muxID, TargetID: plantID},
		{SourceID: plantID, TargetID: demuxID},
		{SourceID: demuxID, SourcePort: 2, TargetID: controllerID},
	} {
		if _, err := service.Connect(ctx, flowID, wire); err != nil {
			t.Fatal(err)
		}
	}
	w1 := NamedChannelRef{
		BlockID: muxID, Direction: ChannelInput, Port: 0, ChannelName: "w1",
	}
	w2 := NamedChannelRef{
		BlockID: muxID, Direction: ChannelInput, Port: 1, ChannelName: "w2",
	}
	uPlant := NamedChannelRef{
		BlockID: muxID, Direction: ChannelInput, Port: 2, ChannelName: "u",
	}
	z1 := NamedChannelRef{
		BlockID: demuxID, Direction: ChannelOutput, Port: 0, ChannelName: "z1",
	}
	z2 := NamedChannelRef{
		BlockID: demuxID, Direction: ChannelOutput, Port: 1, ChannelName: "z2",
	}
	yPlant := NamedChannelRef{
		BlockID: demuxID, Direction: ChannelOutput, Port: 2, ChannelName: "y",
	}
	yController := NamedChannelRef{
		BlockID: controllerID, Direction: ChannelInput, Port: 0, ChannelName: "y",
	}
	uController := NamedChannelRef{
		BlockID: controllerID, Direction: ChannelOutput, Port: 0, ChannelName: "u",
	}
	_, err = service.AssignControlRoles(ctx, flowID, ControlRoleSpec{
		Version: controlRoleSpecVersion,
		Plant: PlantRole{
			Blocks:             []int64{muxID, plantID, demuxID},
			ExogenousInputs:    []NamedChannelRef{w1, w2},
			ControlInputs:      []NamedChannelRef{uPlant},
			PerformanceOutputs: []NamedChannelRef{z1, z2},
			MeasurementOutputs: []NamedChannelRef{yPlant},
		},
		Controller: ControllerRole{
			Blocks:             []int64{controllerID},
			MeasurementInputs:  []NamedChannelRef{yController},
			ControlOutputs:     []NamedChannelRef{uController},
			FeedbackConvention: FeedbackSignedControlLaw,
		},
		AnalysisPoints: []AnalysisPointRole{
			{
				Name: "actuator", Location: AnalysisPointPlantInput,
				Pairs: []LoopBreakPair{{
					Output: uController, Input: uPlant,
				}},
			},
			{
				Name: "sensor", Location: AnalysisPointPlantOutput,
				Pairs: []LoopBreakPair{{
					Output: yPlant, Input: yController,
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, flowID, controllerID
}

func independentContinuousH2Norm(
	t *testing.T,
	system *controlsys.System,
) float64 {
	t.Helper()
	n, _, outputs := system.Dims()
	if system.IsDescriptor() || !system.IsContinuous() ||
		system.HasDelay() || system.HasInternalDelay() {
		t.Fatal("independent H2 oracle requires standard delay-free continuous state space")
	}
	if matrixMaxAbs(system.D) > 1e-12 {
		t.Fatal("independent H2 fixture requires zero direct feedthrough")
	}
	q := mat.NewDense(n, n, nil)
	q.Mul(system.B, system.B.T())
	operator := mat.NewDense(n*n, n*n, nil)
	rhs := mat.NewDense(n*n, 1, nil)
	for row := range n {
		for column := range n {
			equation := row*n + column
			rhs.Set(equation, 0, -q.At(row, column))
			for k := range n {
				operator.Set(
					equation, k*n+column,
					operator.At(equation, k*n+column)+system.A.At(row, k),
				)
				operator.Set(
					equation, row*n+k,
					operator.At(equation, row*n+k)+system.A.At(column, k),
				)
			}
		}
	}
	var solution mat.Dense
	if err := solution.Solve(operator, rhs); err != nil {
		t.Fatalf("independent Lyapunov solve: %v", err)
	}
	gramian := mat.NewDense(n, n, nil)
	for row := range n {
		for column := range n {
			gramian.Set(row, column, solution.At(row*n+column, 0))
		}
	}
	var cGramian, outputCovariance mat.Dense
	cGramian.Mul(system.C, gramian)
	outputCovariance.Mul(&cGramian, system.C.T())
	trace := 0.0
	for output := range outputs {
		trace += outputCovariance.At(output, output)
	}
	return math.Sqrt(math.Max(0, trace))
}

func independentMIMODensePeak(
	t *testing.T,
	system *controlsys.System,
) float64 {
	t.Helper()
	_, inputs, outputs := system.Dims()
	if inputs != 2 || outputs != 2 {
		t.Fatal("dense peak fixture must be 2x2")
	}
	omega := make([]float64, 4001)
	for index := range omega {
		omega[index] = math.Pow(10, -5+10*float64(index)/float64(len(omega)-1))
	}
	response, err := system.FreqResponse(omega)
	if err != nil {
		t.Fatal(err)
	}
	peak := 0.0
	for index := range omega {
		h00 := response.At(index, 0, 0)
		h01 := response.At(index, 0, 1)
		h10 := response.At(index, 1, 0)
		h11 := response.At(index, 1, 1)
		a := cmplx.Abs(h00)*cmplx.Abs(h00) +
			cmplx.Abs(h10)*cmplx.Abs(h10)
		d := cmplx.Abs(h01)*cmplx.Abs(h01) +
			cmplx.Abs(h11)*cmplx.Abs(h11)
		b := cmplx.Conj(h00)*h01 + cmplx.Conj(h10)*h11
		largest := 0.5 * (a + d + math.Sqrt(
			(a-d)*(a-d)+4*cmplx.Abs(b)*cmplx.Abs(b),
		))
		peak = math.Max(peak, math.Sqrt(math.Max(0, largest)))
	}
	return peak
}

func matrixMaxAbs(matrix mat.Matrix) float64 {
	rows, columns := matrix.Dims()
	maximum := 0.0
	for row := range rows {
		for column := range columns {
			maximum = math.Max(maximum, math.Abs(matrix.At(row, column)))
		}
	}
	return maximum
}
