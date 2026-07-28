package studio

import (
	"math"
	"math/cmplx"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestModelStudyMinimalRealizationPreservesFrequencyAndSource(t *testing.T) {
	source := mustModelStudySystem(t,
		[]float64{-1, 0, 0, 0, -2, 0, 0, 0, -3},
		[]float64{1, 0, 0.05},
		[]float64{1, 0, 0.05},
		[]float64{0},
	)
	source.InputName = []string{"disturbance"}
	source.OutputName = []string{"measurement"}
	source.StateName = []string{"dominant", "hidden", "weak"}

	study, err := NewStateSpaceModelStudy("identified plant", source)
	if err != nil {
		t.Fatal(err)
	}
	source.A.Set(0, 0, -99)
	source.InputName[0] = "mutated"

	candidate, err := study.Reduce(ModelReductionRequest{Method: ModelMinimalRealization})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.RetainedOrder != 2 {
		t.Fatalf("retained order = %d, want 2", candidate.RetainedOrder)
	}
	if candidate.Source.Order != 3 || candidate.Source.Name != "identified plant" {
		t.Fatalf("source provenance = %#v", candidate.Source)
	}
	if candidate.InputNames[0] != "disturbance" || candidate.OutputNames[0] != "measurement" {
		t.Fatalf("candidate channel names = %v -> %v", candidate.InputNames, candidate.OutputNames)
	}
	if got := study.SourceSystem().A.At(0, 0); got != -1 {
		t.Fatalf("owned source A(0,0) = %g, want -1", got)
	}

	for _, omega := range []float64{0, 0.01, 0.1, 1, 10, 100} {
		response, err := candidate.System.EvalFr(complex(0, omega))
		if err != nil {
			t.Fatal(err)
		}
		want := 1/complex(1, omega) + 0.0025/complex(3, omega)
		if difference := cmplx.Abs(response[0][0] - want); difference > 2e-11 {
			t.Fatalf("omega %g response = %v, oracle %v, difference %g", omega, response[0][0], want, difference)
		}
	}
	if candidate.FrequencyError.MaxFrobeniusError > 2e-11 {
		t.Fatalf("frequency equivalence error = %g", candidate.FrequencyError.MaxFrobeniusError)
	}
}

func TestModelStudyBalancedTruncationCarriesDiscardedHSVBound(t *testing.T) {
	source := mustModelStudySystem(t,
		[]float64{-1, 0, 0, 0, -4, 0, 0, 0, -20},
		[]float64{1, 0.4, 0.02},
		[]float64{1.5, 0.3, 0.01},
		[]float64{0},
	)
	source.InputName = []string{"force"}
	source.OutputName = []string{"position"}

	study, err := NewStateSpaceModelStudy("flexible mode plant", source)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := study.Reduce(ModelReductionRequest{
		Method: ModelBalancedTruncation,
		Order:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.HinfErrorBound == nil {
		t.Fatal("balanced truncation omitted its error bound")
	}
	if len(candidate.HankelSingularValues) != 3 || len(candidate.DiscardedHankelValues) != 1 {
		t.Fatalf("HSV evidence = %v, discarded = %v", candidate.HankelSingularValues, candidate.DiscardedHankelValues)
	}
	wantBound := 2 * candidate.DiscardedHankelValues[0]
	if math.Abs(*candidate.HinfErrorBound-wantBound) > 1e-14 {
		t.Fatalf("bound = %g, want twice discarded HSV %g", *candidate.HinfErrorBound, wantBound)
	}

	maximum := 0.0
	for index := range 6001 {
		var omega float64
		if index > 0 {
			omega = math.Pow(10, -5+10*float64(index-1)/5999)
		}
		original := diagonalSISOResponse(
			[]float64{-1, -4, -20},
			[]float64{1, 0.4, 0.02},
			[]float64{1.5, 0.3, 0.01},
			omega,
		)
		reduced, err := candidate.System.EvalFr(complex(0, omega))
		if err != nil {
			t.Fatal(err)
		}
		maximum = math.Max(maximum, cmplx.Abs(original-reduced[0][0]))
	}
	if maximum > *candidate.HinfErrorBound*(1+2e-6)+1e-12 {
		t.Fatalf("dense Hinf error %g exceeds balanced-truncation bound %g", maximum, *candidate.HinfErrorBound)
	}
	if candidate.InputNames[0] != "force" || candidate.OutputNames[0] != "position" || candidate.SampleTime != 0 {
		t.Fatalf("candidate metadata lost: %#v", candidate)
	}
}

func TestModelStudyEnergyMatchesIndependentOracles(t *testing.T) {
	source := mustModelStudySystem(t,
		[]float64{-1, 0, 0, -3},
		[]float64{1, 2},
		[]float64{2, -1},
		[]float64{0},
	)
	study, err := NewStateSpaceModelStudy("energy oracle", source)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := study.Energy(ModelEnergyRequest{
		InputNoiseCovariance: mat.NewDense(1, 1, []float64{0.25}),
	})
	if err != nil {
		t.Fatal(err)
	}

	a := []float64{-1, -3}
	b := []float64{1, 2}
	c := []float64{2, -1}
	h2Squared := 0.0
	for row := range 2 {
		for column := range 2 {
			observabilityGramian := -c[row] * c[column] / (a[row] + a[column])
			h2Squared += b[row] * observabilityGramian * b[column]
		}
	}
	wantH2 := math.Sqrt(h2Squared)
	if math.Abs(evidence.H2Norm-wantH2) > 2e-12 {
		t.Fatalf("H2 = %.15g, independent Lyapunov oracle %.15g", evidence.H2Norm, wantH2)
	}

	denseHinf := 0.0
	for index := range 30001 {
		omega := math.Pow(10, -8+16*float64(index)/30000)
		denseHinf = math.Max(denseHinf, cmplx.Abs(diagonalSISOResponse(a, b, c, omega)))
	}
	denseHinf = math.Max(denseHinf, cmplx.Abs(diagonalSISOResponse(a, b, c, 0)))
	if math.Abs(evidence.HinfNorm-denseHinf) > 2e-7 {
		t.Fatalf("Hinf = %.12g, dense sweep %.12g", evidence.HinfNorm, denseHinf)
	}

	stateCovariance := mat.NewDense(2, 2, nil)
	for row := range 2 {
		for column := range 2 {
			stateCovariance.Set(row, column, -b[row]*0.25*b[column]/(a[row]+a[column]))
			residual := (a[row]+a[column])*stateCovariance.At(row, column) +
				b[row]*0.25*b[column]
			if math.Abs(residual) > 2e-14 {
				t.Fatalf("continuous Lyapunov residual(%d,%d) = %g", row, column, residual)
			}
		}
	}
	var outputVariance mat.Dense
	outputVariance.Mul(mat.NewDense(1, 2, c), stateCovariance)
	var projected mat.Dense
	projected.Mul(&outputVariance, mat.NewDense(2, 1, c))
	if got, want := evidence.OutputCovariance[0][0], projected.At(0, 0); math.Abs(got-want) > 2e-12 {
		t.Fatalf("output covariance = %.15g, Lyapunov residual oracle %.15g", got, want)
	}
	if evidence.BalancedRealization == nil || len(evidence.BalancedSingular) != 2 ||
		len(evidence.HankelSingular) != 2 {
		t.Fatalf("balancing evidence incomplete: %#v", evidence)
	}
	for index := range evidence.HankelSingular {
		if math.Abs(evidence.HankelSingular[index]-evidence.BalancedSingular[index]) > 2e-10 {
			t.Fatalf("HSV path disagreement: %v vs %v", evidence.HankelSingular, evidence.BalancedSingular)
		}
	}
}

func TestModelStudyStabilitySeparationPreservesPolesAndTransfer(t *testing.T) {
	source := mustModelStudySystem(t,
		[]float64{-2, 0, 0, 1},
		[]float64{1, 1},
		[]float64{2, 3},
		[]float64{0.5},
	)
	source.InputName = []string{"u"}
	source.OutputName = []string{"y"}
	study, err := NewStateSpaceModelStudy("mixed dynamics", source)
	if err != nil {
		t.Fatal(err)
	}

	decomposition, err := study.SeparateStability()
	if err != nil {
		t.Fatal(err)
	}
	if decomposition.Stable.Order != 1 || decomposition.Unstable.Order != 1 {
		t.Fatalf("component orders = %d stable, %d unstable", decomposition.Stable.Order, decomposition.Unstable.Order)
	}
	if len(decomposition.Stable.Poles) != 1 || math.Abs(decomposition.Stable.Poles[0].Real+2) > 1e-10 {
		t.Fatalf("stable poles = %v", decomposition.Stable.Poles)
	}
	if len(decomposition.Unstable.Poles) != 1 || math.Abs(decomposition.Unstable.Poles[0].Real-1) > 1e-10 {
		t.Fatalf("unstable poles = %v", decomposition.Unstable.Poles)
	}

	for _, omega := range []float64{0, 0.2, 1, 7, 50} {
		original, err := source.EvalFr(complex(0, omega))
		if err != nil {
			t.Fatal(err)
		}
		stable, err := decomposition.Stable.System.EvalFr(complex(0, omega))
		if err != nil {
			t.Fatal(err)
		}
		unstable, err := decomposition.Unstable.System.EvalFr(complex(0, omega))
		if err != nil {
			t.Fatal(err)
		}
		if difference := cmplx.Abs(original[0][0] - stable[0][0] - unstable[0][0]); difference > 2e-10 {
			t.Fatalf("G != Gs+Gu at omega %g: difference %g", omega, difference)
		}
	}
}

func TestModelStudyPassivityUsesSampledHermitianEigenvalueEvidence(t *testing.T) {
	omega := []float64{0, 0.5, 2, 10}
	response := make([][][]complex128, len(omega))
	for index := range response {
		response[index] = [][]complex128{{2, 0.5}, {0.5, 1}}
	}
	frd, err := controlsys.NewFRD(response, omega, 0)
	if err != nil {
		t.Fatal(err)
	}
	frd.InputName = []string{"left", "right"}
	frd.OutputName = []string{"left", "right"}
	study, err := NewFRDModelStudy("measured impedance", frd)
	if err != nil {
		t.Fatal(err)
	}

	evidence, err := study.Passivity(&controlsys.PassivityOptions{Tol: 1e-12})
	if err != nil {
		t.Fatal(err)
	}
	wantMinimum := (3 - math.Sqrt(2)) / 2
	if math.Abs(evidence.MinHermitianEigenvalue-wantMinimum) > 2e-12 {
		t.Fatalf("minimum Hermitian eigenvalue = %.15g, oracle %.15g", evidence.MinHermitianEigenvalue, wantMinimum)
	}
	if !evidence.Passive || evidence.Status != controlsys.PassivitySampled ||
		!evidence.SampledEvidence || evidence.AnalyticCertificate {
		t.Fatalf("passivity guarantee overstated: %#v", evidence)
	}

	gain, err := controlsys.NewGain(mat.NewDense(2, 2, []float64{2, 0.5, 0.5, 1}), 0)
	if err != nil {
		t.Fatal(err)
	}
	systemStudy, err := NewStateSpaceModelStudy("static impedance", gain)
	if err != nil {
		t.Fatal(err)
	}
	systemEvidence, err := systemStudy.Passivity(&controlsys.PassivityOptions{Omega: omega})
	if err != nil {
		t.Fatal(err)
	}
	if systemEvidence.Status != controlsys.PassivitySampled || systemEvidence.AnalyticCertificate {
		t.Fatalf("state-space sampled passivity guarantee = %#v", systemEvidence)
	}
}

func TestModelStudyRoutesReductionFamilies(t *testing.T) {
	nonminimal := mustModelStudySystem(t,
		[]float64{-1, 0, 0, 0, -2, 0, 0, 0, -5},
		[]float64{1, 0, 0.2},
		[]float64{1, 0, 0.3},
		[]float64{0},
	)
	nonminimalStudy, err := NewStateSpaceModelStudy("nonminimal", nonminimal)
	if err != nil {
		t.Fatal(err)
	}
	staircase, err := nonminimalStudy.Reduce(ModelReductionRequest{
		Method: ModelStaircaseReduction, ReduceMode: controlsys.ReduceAll, Equalize: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if staircase.RetainedOrder != 2 {
		t.Fatalf("staircase retained order = %d, want 2", staircase.RetainedOrder)
	}

	minimal := mustModelStudySystem(t,
		[]float64{-1, 0, 0, 0, -3, 0, 0, 0, -8},
		[]float64{1, 0.5, 0.1},
		[]float64{1, 0.4, 0.2},
		[]float64{0},
	)
	study, err := NewStateSpaceModelStudy("minimal", minimal)
	if err != nil {
		t.Fatal(err)
	}
	tests := []ModelReductionRequest{
		{Method: ModelBalancedResidualization, Order: 2},
		{Method: ModelStateTruncation, Eliminate: []int{2}},
		{Method: ModelStateResidualization, Eliminate: []int{2}},
		{Method: ModelModalTruncation, Order: 2},
	}
	for _, request := range tests {
		t.Run(string(request.Method), func(t *testing.T) {
			candidate, err := study.Reduce(request)
			if err != nil {
				t.Fatal(err)
			}
			if candidate.RetainedOrder != 2 {
				t.Fatalf("retained order = %d, want 2", candidate.RetainedOrder)
			}
			if candidate.System == minimal || candidate.System == study.system {
				t.Fatal("candidate aliases its source")
			}
			if len(candidate.StateNames) != 2 {
				t.Fatalf("candidate state names = %v", candidate.StateNames)
			}
		})
	}
}

func TestModelStudySurfacesDescriptorAndDelayLimitationsBeforeAlgorithms(t *testing.T) {
	descriptor, err := controlsys.NewDescriptor(
		mat.NewDense(1, 1, []float64{-1}),
		mat.NewDense(1, 1, []float64{1}),
		mat.NewDense(1, 1, []float64{1}),
		mat.NewDense(1, 1, []float64{0}),
		mat.NewDense(1, 1, []float64{2}),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	descriptorStudy, err := NewStateSpaceModelStudy("descriptor", descriptor)
	if err != nil {
		t.Fatal(err)
	}
	assertUnavailable := func(t *testing.T, study *ModelStudy, capability ModelStudyCapability, want string) {
		t.Helper()
		assessment := study.Capability(capability)
		if assessment.Available || !strings.Contains(assessment.Limitation, want) {
			t.Fatalf("%s assessment = %#v", capability, assessment)
		}
	}
	assertUnavailable(t, descriptorStudy, ModelStudyReduction, "descriptor")
	if _, err := descriptorStudy.Reduce(ModelReductionRequest{Method: ModelMinimalRealization}); err == nil ||
		!strings.Contains(err.Error(), "descriptor") {
		t.Fatalf("descriptor reduction error = %v", err)
	}

	delayed := mustModelStudySystem(t,
		[]float64{-1},
		[]float64{1},
		[]float64{1},
		[]float64{0},
	)
	delayed.Delay = mat.NewDense(1, 1, []float64{0.25})
	delayedStudy, err := NewStateSpaceModelStudy("delayed", delayed)
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range []ModelStudyCapability{
		ModelStudyReduction,
		ModelStudyEnergy,
		ModelStudyStabilitySep,
		ModelStudyCovariance,
		ModelStudyPassivity,
	} {
		assertUnavailable(t, delayedStudy, capability, "exact delays")
	}
	if _, err := delayedStudy.Energy(ModelEnergyRequest{}); err == nil || !strings.Contains(err.Error(), "exact delays") {
		t.Fatalf("delayed energy error = %v", err)
	}

	frd, err := controlsys.NewFRD(
		[][][]complex128{{{1}}},
		[]float64{0},
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	frdStudy, err := NewFRDModelStudy("measurement", frd)
	if err != nil {
		t.Fatal(err)
	}
	assertUnavailable(t, frdStudy, ModelStudyEnergy, "state-space")
}

func BenchmarkModelStudyRepresentativeReduction(b *testing.B) {
	source := benchmarkModelStudySystem(b, 8)
	study, err := NewStateSpaceModelStudy("benchmark", source)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, err := study.Reduce(ModelReductionRequest{
			Method: ModelBalancedTruncation,
			Order:  4,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkModelStudyRepresentativeEvidence(b *testing.B) {
	source := benchmarkModelStudySystem(b, 6)
	study, err := NewStateSpaceModelStudy("benchmark", source)
	if err != nil {
		b.Fatal(err)
	}
	noise := mat.NewDense(2, 2, []float64{1, 0.1, 0.1, 0.5})
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, err := study.Energy(ModelEnergyRequest{InputNoiseCovariance: noise})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func mustModelStudySystem(
	t testing.TB,
	a, b, c, d []float64,
) *controlsys.System {
	t.Helper()
	n := int(math.Sqrt(float64(len(a))))
	inputs := len(b) / n
	outputs := len(c) / n
	system, err := controlsys.New(
		mat.NewDense(n, n, a),
		mat.NewDense(n, inputs, b),
		mat.NewDense(outputs, n, c),
		mat.NewDense(outputs, inputs, d),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	return system
}

func diagonalSISOResponse(a, b, c []float64, omega float64) complex128 {
	var response complex128
	for index := range a {
		response += complex(b[index]*c[index], 0) / (complex(0, omega) - complex(a[index], 0))
	}
	return response
}

func benchmarkModelStudySystem(t testing.TB, order int) *controlsys.System {
	t.Helper()
	a := make([]float64, order*order)
	b := make([]float64, order*2)
	c := make([]float64, 2*order)
	for index := range order {
		a[index*order+index] = -float64(index + 1)
		b[index*2] = 1 / float64(index+1)
		b[index*2+1] = 0.5 / float64(index+1)
		c[index] = 0.75 / float64(index+1)
		c[order+index] = 0.25 / float64(index+1)
	}
	system, err := controlsys.New(
		mat.NewDense(order, order, a),
		mat.NewDense(order, 2, b),
		mat.NewDense(2, order, c),
		mat.NewDense(2, 2, nil),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	return system
}
