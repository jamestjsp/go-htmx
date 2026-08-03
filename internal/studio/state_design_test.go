package studio

import (
	"context"
	"math"
	"math/cmplx"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestDesignContinuousLQRHasSmallIndependentRiccatiResidualAndAtomicApply(t *testing.T) {
	service, flowID, _, controllerID := stateDesignStudio(
		t, modelDomainContinuous, BlockMatrixGain,
	)
	ctx := context.Background()
	q := testMatrix(t, 2, 2, []float64{2, 0, 0, 1})
	r := testMatrix(t, 1, 1, []float64{0.5})
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := service.DesignStateFeedback(ctx, flowID, StateFeedbackRequest{
		Method: StateFeedbackLQR, Q: &q, R: &r,
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.GainK == nil || candidate.RiccatiX == nil ||
		candidate.Controller == nil ||
		!candidate.Diagnostics.Controllable ||
		!candidate.Diagnostics.Observable {
		t.Fatalf("LQR candidate = %#v", candidate)
	}
	models, err := service.BuildControlModels(ctx, flowID, ControlModelBuildRequest{})
	if err != nil {
		t.Fatal(err)
	}
	x := denseMatrix(candidate.RiccatiX)
	k := denseMatrix(candidate.GainK)
	residual := careResidual(models.Plant.A, models.Plant.B, denseMatrix(&q), denseMatrix(&r), x)
	if normalizedDenseNorm(residual) > 1e-10 {
		t.Fatalf("normalized CARE residual = %g", normalizedDenseNorm(residual))
	}
	closedA := mat.NewDense(2, 2, nil)
	closedA.Mul(models.Plant.B, k)
	closedA.Sub(models.Plant.A, closedA)
	eigenvalues, err := denseEigenvalues(closedA)
	if err != nil {
		t.Fatal(err)
	}
	for _, pole := range eigenvalues {
		if real(pole) >= 0 {
			t.Fatalf("LQR pole = %v", pole)
		}
	}
	unmodified, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if unmodified.Flow.ModelUpdatedAt != before.Flow.ModelUpdatedAt {
		t.Fatal("state candidate generation mutated the model")
	}
	application, err := service.ApplyStateDesignCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	applied := application.Snapshot
	controller := findBlock(t, applied.Blocks, controllerID)
	for column := range 2 {
		if math.Abs(controller.Parameters.D.At(0, column)+k.At(0, column)) > 1e-12 {
			t.Fatalf("authored -K[%d] = %g, K = %g", column, controller.Parameters.D.At(0, column), k.At(0, column))
		}
	}
	appliedModels, err := service.BuildControlModels(
		ctx, flowID, ControlModelBuildRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for column := range 2 {
		if math.Abs(appliedModels.Controller.D.At(0, column)-k.At(0, column)) > 1e-12 {
			t.Fatalf(
				"signed control-law normalization[%d] = %g, want +K %g",
				column, appliedModels.Controller.D.At(0, column), k.At(0, column),
			)
		}
	}
	if _, err := service.ApplyStateDesignCandidate(ctx, candidate); err == nil ||
		!strings.Contains(err.Error(), "stale") {
		t.Fatalf("second state apply error = %v", err)
	}
}

func TestStateFeedbackPolePlacementLQIAndLQRDRouting(t *testing.T) {
	service, flowID, _, _ := stateDesignStudio(
		t, modelDomainContinuous, BlockMatrixGain,
	)
	ctx := context.Background()
	placed, err := service.DesignStateFeedback(ctx, flowID, StateFeedbackRequest{
		Method: StateFeedbackPlace,
		Poles:  []ComplexValue{{Real: -2}, {Real: -5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertComplexMultiset(t, placed.ClosedLoopPoles, []complex128{-2, -5}, 1e-8)
	acker, err := service.DesignStateFeedback(ctx, flowID, StateFeedbackRequest{
		Method: StateFeedbackAcker,
		Poles:  []ComplexValue{{Real: -3}, {Real: -6}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertComplexMultiset(t, acker.ClosedLoopPoles, []complex128{-3, -6}, 1e-8)

	qLQI := testMatrix(t, 3, 3, []float64{
		1, 0, 0,
		0, 1, 0,
		0, 0, 2,
	})
	regulated := testMatrix(t, 1, 2, []float64{1, 0})
	r := testMatrix(t, 1, 1, []float64{1})
	lqi, err := service.DesignStateFeedback(ctx, flowID, StateFeedbackRequest{
		Method: StateFeedbackLQI, Q: &qLQI, R: &r,
		RegulatedOutput: &regulated,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lqi.Controller == nil {
		t.Fatal("LQI candidate has no integral controller")
	}
	if states, inputs, outputs := lqi.Controller.Dims(); states != 1 || inputs != 3 || outputs != 1 {
		t.Fatalf("LQI controller dimensions = n%d %d×%d", states, outputs, inputs)
	}
	if !equalStrings(
		lqi.Controller.StateName, []string{"integral.regulated1"},
	) {
		t.Fatalf("LQI state names = %v", lqi.Controller.StateName)
	}

	q := testMatrix(t, 2, 2, []float64{1, 0, 0, 1})
	lqrd, err := service.DesignStateFeedback(ctx, flowID, StateFeedbackRequest{
		Method: StateFeedbackLQRD, Q: &q, R: &r, SampleTime: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lqrd.Controller == nil || lqrd.Controller.Dt != 0.1 ||
		len(lqrd.Warnings) == 0 || lqrd.edit != nil {
		t.Fatalf("LQRD sampled candidate = %#v", lqrd)
	}
}

func TestDiscreteLQRAndKalmanFollowPlantDomain(t *testing.T) {
	service, flowID, _, _ := stateDesignStudio(
		t, modelDomainDiscrete, BlockMatrixGain,
	)
	q := testMatrix(t, 2, 2, []float64{1, 0, 0, 1})
	r := testMatrix(t, 1, 1, []float64{1})
	lqr, err := service.DesignStateFeedback(
		context.Background(), flowID,
		StateFeedbackRequest{Method: StateFeedbackLQR, Q: &q, R: &r},
	)
	if err != nil {
		t.Fatal(err)
	}
	if lqr.Controller.Dt != 0.1 {
		t.Fatalf("discrete state-feedback Dt = %g", lqr.Controller.Dt)
	}
	for _, pole := range lqr.ClosedLoopPoles {
		if cmplx.Abs(complex(pole.Real, pole.Imag)) >= 1 {
			t.Fatalf("discrete LQR pole = %#v", pole)
		}
	}
	qn := testMatrix(t, 1, 1, []float64{0.2})
	rn := testMatrix(t, 2, 2, []float64{0.1, 0, 0, 0.1})
	kalman, err := service.DesignEstimator(
		context.Background(), flowID,
		EstimatorDesignRequest{
			Method: EstimatorKalman, Qn: &qn, Rn: &rn,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if kalman.Estimator == nil || kalman.Estimator.Dt != 0.1 ||
		kalman.GainL == nil {
		t.Fatalf("discrete Kalman candidate = %#v", kalman)
	}
	for _, pole := range kalman.EstimatorPoles {
		if cmplx.Abs(complex(pole.Real, pole.Imag)) >= 1 {
			t.Fatalf("discrete estimator pole = %#v", pole)
		}
	}
}

func TestLQEKalmdAndLQGProduceNamedEstimatorAndRegulatorCandidates(t *testing.T) {
	service, flowID, _, controllerID := stateDesignStudio(
		t, modelDomainContinuous, BlockStateSpace,
	)
	qn := testMatrix(t, 1, 1, []float64{0.2})
	rn := testMatrix(t, 2, 2, []float64{0.1, 0, 0, 0.1})
	g := testMatrix(t, 2, 1, []float64{0, 1})
	lqe, err := service.DesignEstimator(
		context.Background(), flowID,
		EstimatorDesignRequest{
			Method: EstimatorLQE, Qn: &qn, Rn: &rn, G: &g,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if lqe.Estimator == nil ||
		!equalStrings(
			lqe.Estimator.InputName,
			[]string{"command.u", "measurement.x1", "measurement.x2"},
		) ||
		!equalStrings(
			lqe.Estimator.StateName,
			[]string{"estimate-state.x1", "estimate-state.x2"},
		) {
		t.Fatalf("named LQE estimator = %#v", lqe.Estimator)
	}
	models, err := service.BuildControlModels(
		context.Background(), flowID, ControlModelBuildRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if residual := estimatorCareResidual(
		models.Plant.A, models.Plant.C, denseMatrix(&g),
		denseMatrix(&qn), denseMatrix(&rn), denseMatrix(lqe.RiccatiX),
	); normalizedDenseNorm(residual) > 1e-9 {
		t.Fatalf("normalized estimator CARE residual = %g", normalizedDenseNorm(residual))
	}
	observer, err := service.DesignEstimator(
		context.Background(), flowID,
		EstimatorDesignRequest{
			Method: EstimatorPlace,
			Poles:  []ComplexValue{{Real: -4}, {Real: -7}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertComplexMultiset(t, observer.EstimatorPoles, []complex128{-4, -7}, 1e-8)
	kalmd, err := service.DesignEstimator(
		context.Background(), flowID,
		EstimatorDesignRequest{
			Method: EstimatorKalmd, Qn: &qn, Rn: &rn, SampleTime: 0.1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if kalmd.Estimator.Dt != 0.1 || len(kalmd.Warnings) == 0 {
		t.Fatalf("Kalmd candidate = %#v", kalmd)
	}

	q := testMatrix(t, 2, 2, []float64{2, 0, 0, 1})
	r := testMatrix(t, 1, 1, []float64{0.5})
	lqg, err := service.DesignObserverRegulator(
		context.Background(), flowID,
		ObserverRegulatorRequest{
			Method: ObserverRegulatorLQG,
			Q:      &q, R: &r, Qn: &qn, Rn: &rn,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if lqg.Controller == nil || lqg.GainK == nil || lqg.GainL == nil ||
		!equalStrings(lqg.Controller.InputName, []string{"x1", "x2"}) ||
		!equalStrings(lqg.Controller.OutputName, []string{"u"}) {
		t.Fatalf("LQG candidate = %#v", lqg)
	}
	explicit, err := controlsys.Reg(
		models.Plant, denseMatrix(lqg.GainK), denseMatrix(lqg.GainL),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !mat.EqualApprox(explicit.A, lqg.Controller.A, 1e-11) ||
		!mat.EqualApprox(explicit.B, lqg.Controller.B, 1e-11) ||
		!mat.EqualApprox(explicit.C, lqg.Controller.C, 1e-11) {
		t.Fatal("LQG controller differs from independently composed Reg(K,L)")
	}
	regulator, err := service.DesignObserverRegulator(
		context.Background(), flowID,
		ObserverRegulatorRequest{
			Method: ObserverRegulatorReg,
			K:      lqg.GainK, L: lqg.GainL,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !mat.EqualApprox(regulator.Controller.A, lqg.Controller.A, 1e-11) {
		t.Fatal("explicit Reg candidate differs from LQG regulator")
	}
	application, err := service.ApplyStateDesignCandidate(
		context.Background(), lqg,
	)
	if err != nil {
		t.Fatal(err)
	}
	applied := application.Snapshot
	authored := findBlock(t, applied.Blocks, controllerID)
	if !mat.EqualApprox(
		denseMatrix(authored.Parameters.A), lqg.Controller.A, 1e-11,
	) || !equalStrings(
		authored.Parameters.StateNames.Names(),
		[]string{"estimate-state.x1", "estimate-state.x2"},
	) {
		t.Fatalf("applied LQG controller = %#v", authored.Parameters)
	}
}

func TestStateDesignRejectsMalformedCostsPartialStateFeedbackAndWrongDomains(t *testing.T) {
	service, flowID, plantID, _ := stateDesignStudio(
		t, modelDomainContinuous, BlockMatrixGain,
	)
	qBad := testMatrix(t, 2, 2, []float64{1, 2, 0, 1})
	r := testMatrix(t, 1, 1, []float64{1})
	if _, err := service.DesignStateFeedback(
		context.Background(), flowID,
		StateFeedbackRequest{Method: StateFeedbackLQR, Q: &qBad, R: &r},
	); err == nil || !strings.Contains(err.Error(), "symmetric") {
		t.Fatalf("nonsymmetric Q error = %v", err)
	}
	q := testMatrix(t, 2, 2, []float64{1, 0, 0, 1})
	if _, err := service.DesignEstimator(
		context.Background(), flowID,
		EstimatorDesignRequest{
			Method: EstimatorLQE,
			Qn:     &r, Rn: &q,
		},
	); err == nil || !strings.Contains(err.Error(), "explicit") {
		t.Fatalf("missing G error = %v", err)
	}
	if _, err := service.DesignEstimator(
		context.Background(), flowID,
		EstimatorDesignRequest{
			Method: EstimatorKalmd, Qn: &r, Rn: &q,
		},
	); err == nil || !strings.Contains(err.Error(), "sample time") {
		t.Fatalf("Kalmd sample-time error = %v", err)
	}
	if _, err := service.UpdateBlock(
		context.Background(), plantID,
		BlockUpdate{
			Name: "Plant",
			Parameters: map[string]string{
				"a": "0, 1; -2, -3", "b": "0; 1",
				"c": "1, 0; 1, 0", "d": "0; 0",
				"input_names": "u", "output_names": "x1, x2",
				"state_names": "x1, x2",
				"time_domain": "continuous", "sample_time": "0.1",
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DesignStateFeedback(
		context.Background(), flowID,
		StateFeedbackRequest{Method: StateFeedbackLQR, Q: &q, R: &r},
	); err == nil || !strings.Contains(err.Error(), "direct state feedback") {
		t.Fatalf("partial state-feedback error = %v", err)
	}
}

func stateDesignStudio(
	t *testing.T,
	domain string,
	controllerKind BlockKind,
) (*Studio, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "state-design.db"))
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFlow(ctx, current.Project.ID, "State design")
	if err != nil {
		t.Fatal(err)
	}
	flowID := created.Snapshot.Flow.ID
	_, plantID, err := service.AddBlock(ctx, flowID, BlockStateSpace, Point{X: 100, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	plantA := "0, 1; -2, -3"
	if domain == modelDomainDiscrete {
		plantA = "0.9, 0.1; 0, 0.8"
	}
	plantParameters := map[string]string{
		"a": plantA, "b": "0; 1", "c": "1, 0; 0, 1", "d": "0; 0",
		"input_names": "u", "output_names": "x1, x2",
		"state_names": "x1, x2", "time_domain": domain,
		"sample_time": "0.1",
	}
	if _, err := service.UpdateBlock(ctx, plantID, BlockUpdate{
		Name: "Plant", Parameters: plantParameters,
	}); err != nil {
		t.Fatal(err)
	}
	_, controllerID, err := service.AddBlock(
		ctx, flowID, controllerKind, Point{X: 500, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	switch controllerKind {
	case BlockMatrixGain:
		if _, err := service.UpdateBlock(ctx, controllerID, BlockUpdate{
			Name: "State feedback",
			Parameters: map[string]string{
				"d": "-1, -1", "input_names": "x1, x2", "output_names": "u",
			},
		}); err != nil {
			t.Fatal(err)
		}
	case BlockStateSpace:
		parameters := map[string]string{
			"a": "-1", "b": "0, 0", "c": "0", "d": "0, 0",
			"input_names": "x1, x2", "output_names": "u",
			"state_names": "estimate-state.x1", "time_domain": domain,
			"sample_time": "0.1",
		}
		if _, err := service.UpdateBlock(ctx, controllerID, BlockUpdate{
			Name: "Observer regulator", Parameters: parameters,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Connect(ctx, flowID, Wire{
		SourceID: controllerID, TargetID: plantID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, flowID, Wire{
		SourceID: plantID, TargetID: controllerID,
	}); err != nil {
		t.Fatal(err)
	}
	plantInputs := namedRefs(plantID, ChannelInput, []string{"u"})
	plantOutputs := namedRefs(plantID, ChannelOutput, []string{"x1", "x2"})
	controllerInputs := namedRefs(controllerID, ChannelInput, []string{"x1", "x2"})
	controllerOutputs := namedRefs(controllerID, ChannelOutput, []string{"u"})
	spec := ControlRoleSpec{
		Version: controlRoleSpecVersion,
		Plant: PlantRole{
			Blocks: []int64{plantID}, ControlInputs: plantInputs,
			MeasurementOutputs: plantOutputs,
		},
		Controller: ControllerRole{
			Blocks:             []int64{controllerID},
			FeedbackConvention: FeedbackSignedControlLaw,
			MeasurementInputs:  controllerInputs, ControlOutputs: controllerOutputs,
		},
		AnalysisPoints: []AnalysisPointRole{
			{
				Name: "actuator", Location: AnalysisPointPlantInput,
				Pairs: loopPairs(controllerOutputs, plantInputs),
			},
			{
				Name: "sensor", Location: AnalysisPointPlantOutput,
				Pairs: loopPairs(plantOutputs, controllerInputs),
			},
		},
	}
	if _, err := service.AssignControlRoles(ctx, flowID, spec); err != nil {
		t.Fatal(err)
	}
	return service, flowID, plantID, controllerID
}

func testMatrix(
	t *testing.T,
	rows, columns int,
	values []float64,
) MatrixValue {
	t.Helper()
	value, err := NewMatrixValue(rows, columns, values)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func careResidual(
	a, b, q, r, x *mat.Dense,
) *mat.Dense {
	var rInverse mat.Dense
	if err := rInverse.Inverse(r); err != nil {
		panic(err)
	}
	left := mat.NewDense(2, 2, nil)
	left.Mul(a.T(), x)
	right := mat.NewDense(2, 2, nil)
	right.Mul(x, a)
	residual := mat.NewDense(2, 2, nil)
	residual.Add(left, right)
	xb := mat.NewDense(2, 1, nil)
	xb.Mul(x, b)
	xbR := mat.NewDense(2, 1, nil)
	xbR.Mul(xb, &rInverse)
	quadratic := mat.NewDense(2, 2, nil)
	quadratic.Mul(xbR, xb.T())
	residual.Sub(residual, quadratic)
	residual.Add(residual, q)
	return residual
}

func estimatorCareResidual(
	a, c, g, qn, rn, x *mat.Dense,
) *mat.Dense {
	var rnInverse mat.Dense
	if err := rnInverse.Inverse(rn); err != nil {
		panic(err)
	}
	ax := mat.NewDense(2, 2, nil)
	ax.Mul(a, x)
	xat := mat.NewDense(2, 2, nil)
	xat.Mul(x, a.T())
	residual := mat.NewDense(2, 2, nil)
	residual.Add(ax, xat)
	xc := mat.NewDense(2, 2, nil)
	xc.Mul(x, c.T())
	xcR := mat.NewDense(2, 2, nil)
	xcR.Mul(xc, &rnInverse)
	quadratic := mat.NewDense(2, 2, nil)
	quadratic.Mul(xcR, xc.T())
	residual.Sub(residual, quadratic)
	gq := mat.NewDense(2, 1, nil)
	gq.Mul(g, qn)
	process := mat.NewDense(2, 2, nil)
	process.Mul(gq, g.T())
	residual.Add(residual, process)
	return residual
}

func normalizedDenseNorm(matrix *mat.Dense) float64 {
	return mat.Norm(matrix, 2) / math.Max(1, mat.Norm(matrix, 1))
}

func assertComplexMultiset(
	t *testing.T,
	got []ComplexValue,
	want []complex128,
	tolerance float64,
) {
	t.Helper()
	used := make([]bool, len(got))
	for _, target := range want {
		found := false
		for i, value := range got {
			if !used[i] && cmplx.Abs(complex(value.Real, value.Imag)-target) <= tolerance {
				used[i], found = true, true
				break
			}
		}
		if !found {
			t.Fatalf("poles = %#v, missing %v", got, target)
		}
	}
}
