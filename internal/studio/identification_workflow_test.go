package studio

import (
	"encoding/json"
	"errors"
	"math"
	"math/cmplx"
	"math/rand"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestIdentificationWorkflowRecoversNoisySISOFrequencyResponse(t *testing.T) {
	const (
		samples = 8192
		dt      = 0.05
		a       = 0.8
		b       = 0.2
	)
	random := rand.New(rand.NewSource(401))
	input := make([]float64, samples)
	output := make([]float64, samples)
	for sample := range samples {
		input[sample] = random.NormFloat64()
		if sample > 0 {
			output[sample] = a*output[sample-1] +
				b*input[sample-1] + 0.025*random.NormFloat64()
		}
	}
	request := FrequencyIdentificationRequest{
		Name: "noisy first-order plant",
		Dataset: identificationTestDataset(
			t, [][]float64{input}, [][]float64{output}, dt,
			[]string{"command"}, []string{"position"},
			IdentificationSplit{
				Training:   SampleRange{Start: 0, End: samples / 2},
				Validation: SampleRange{Start: samples / 2, End: samples},
			},
		),
		Options: FrequencyEstimationOptions{
			Method: FrequencyEstimationH1, Window: IdentificationWindowHann,
			NFFT: 256, Overlap: 128, MinCoherence: 0.65,
		},
	}

	candidate, err := NewIdentificationWorkflow().EstimateFrequencyResponse(request)
	if err != nil {
		t.Fatal(err)
	}
	frd, err := candidate.FRD()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := frd.InputName, []string{"command"}; !identificationEqualStrings(got, want) {
		t.Fatalf("input names = %v, want %v", got, want)
	}
	if got, want := frd.OutputName, []string{"position"}; !identificationEqualStrings(got, want) {
		t.Fatalf("output names = %v, want %v", got, want)
	}
	var relativeErrors []float64
	for frequency, omega := range frd.Omega {
		if omega == 0 || omega > 0.8*math.Pi/dt ||
			candidate.Coherence[frequency] < 0.8 {
			continue
		}
		zInverse := cmplx.Exp(complex(0, -omega*dt))
		want := complex(b, 0) * zInverse / (1 - complex(a, 0)*zInverse)
		relativeErrors = append(
			relativeErrors,
			cmplx.Abs(frd.Response[frequency][0][0]-want)/cmplx.Abs(want),
		)
	}
	if len(relativeErrors) < 60 {
		t.Fatalf("only %d analytically comparable frequency bins", len(relativeErrors))
	}
	if got := median(relativeErrors); got > 0.08 {
		t.Fatalf("median analytic relative error = %.4f, want <= 0.08", got)
	}
	if candidate.Diagnostics.InputRank != 1 {
		t.Fatalf("input rank = %d, want 1", candidate.Diagnostics.InputRank)
	}
	if candidate.Diagnostics.MeanCoherence <= 0.75 ||
		candidate.Diagnostics.MeanCoherence >= 1 {
		t.Fatalf(
			"mean coherence = %.5f, want noisy evidence strictly between 0.75 and 1",
			candidate.Diagnostics.MeanCoherence,
		)
	}
	if candidate.Fit.ComparedBins == 0 || candidate.Fit.FitPercent <= 70 {
		t.Fatalf("validation fit = %+v, want held-out frequency evidence", candidate.Fit)
	}
	if candidate.Provenance.Preprocessing != PreprocessingRemoveMean {
		t.Fatalf("preprocessing provenance = %q", candidate.Provenance.Preprocessing)
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		t.Fatalf("marshal persisted candidate: %v", err)
	}
	var restored FRDCandidate
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("unmarshal persisted candidate: %v", err)
	}
	if restored.Provenance.Estimator == nil ||
		restored.Provenance.Estimator.Window != IdentificationWindowHann ||
		restored.Provenance.Estimator.Method != FrequencyEstimationH1 {
		t.Fatalf("restored estimator provenance = %+v", restored.Provenance.Estimator)
	}
	if _, err := restored.FRD(); err != nil {
		t.Fatalf("restore candidate FRD: %v", err)
	}
}

func TestIdentificationWorkflowRefusesRankDeficientMIMOExcitation(t *testing.T) {
	const samples = 1024
	first := make([]float64, samples)
	second := make([]float64, samples)
	output1 := make([]float64, samples)
	output2 := make([]float64, samples)
	for index := range samples {
		first[index] = math.Sin(0.07*float64(index)) + math.Cos(0.19*float64(index))
		second[index] = 2 * first[index]
		output1[index] = first[index] + second[index]
		output2[index] = first[index] - second[index]
	}
	dataset := identificationTestDataset(
		t,
		[][]float64{first, second}, [][]float64{output1, output2}, 0.1,
		[]string{"force", "torque"}, []string{"position", "angle"},
		IdentificationSplit{
			Training:   SampleRange{Start: 0, End: samples / 2},
			Validation: SampleRange{Start: samples / 2, End: samples},
		},
	)
	dataset.InputUnits = []string{"N", "N m"}
	dataset.OutputUnits = []string{"m", "rad"}

	_, err := NewIdentificationWorkflow().EstimateFrequencyResponse(
		FrequencyIdentificationRequest{
			Name:    "rank deficient",
			Dataset: dataset,
			Options: FrequencyEstimationOptions{
				Method: FrequencyEstimationH1, Window: IdentificationWindowHann,
				NFFT: 128, Overlap: 64, MinCoherence: 0.5,
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "input rank is 1 for 2 channels") {
		t.Fatalf("error = %v, want explicit MIMO excitation-rank refusal", err)
	}
}

func TestIdentificationWorkflowERAUsesHeldOutMarkovParameters(t *testing.T) {
	a := mat.NewDense(2, 2, []float64{0.7, 0.1, 0, 0.35})
	b := mat.NewDense(2, 1, []float64{1, 0.4})
	c := mat.NewDense(1, 2, []float64{0.8, -0.2})
	d := mat.NewDense(1, 1, []float64{0.05})
	system, err := controlsys.New(a, b, c, d, 0.1)
	if err != nil {
		t.Fatal(err)
	}
	parameters := make([]MatrixValue, 32)
	for index := range parameters {
		value := systemMarkovParameter(system, index)
		parameters[index], err = NewMatrixValue(1, 1, []float64{value.At(0, 0)})
		if err != nil {
			t.Fatal(err)
		}
	}

	candidate, err := NewIdentificationWorkflow().IdentifyERA(
		ERAIdentificationRequest{
			Name: "two-mode impulse model",
			Dataset: MarkovParameterDataset{
				Parameters: parameters, TrainingCount: 19,
				InputNames: []string{"impulse"}, OutputNames: []string{"response"},
				InputUnits: []string{"N"}, OutputUnits: []string{"m"},
				SampleTime: 0.1, TimeUnit: "s",
			},
			Order: 2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Order != 2 || len(candidate.HankelSingularValues) < 2 {
		t.Fatalf("ERA candidate order/HSV = %d/%v", candidate.Order, candidate.HankelSingularValues)
	}
	if candidate.Fit.HeldOutParameters != 13 {
		t.Fatalf("held-out count = %d, want 13", candidate.Fit.HeldOutParameters)
	}
	if candidate.Fit.RelativeRMS > 1e-10 || candidate.Fit.FitPercent < 99.999999 {
		t.Fatalf("held-out ERA fit = %+v", candidate.Fit)
	}
	if got := candidate.Model.InputNames; !identificationEqualStrings(got, []string{"impulse"}) {
		t.Fatalf("identified input names = %v", got)
	}
	if got := candidate.Provenance.Split.Validation; got != (SampleRange{Start: 19, End: 32}) {
		t.Fatalf("validation provenance = %+v", got)
	}
}

func TestIdentificationWorkflowFRDAlgebraMatchesDirectComplexMatrices(t *testing.T) {
	omega := []float64{0.2, 1.1, 4.0}
	plantResponses := [][][]complex128{
		{{1 + 0.2i, 0.1 - 0.1i}, {0.3i, 0.8 - 0.2i}},
		{{0.7 - 0.4i, 0.2}, {-0.1 + 0.2i, 0.6 - 0.3i}},
		{{0.2 - 0.3i, 0.05i}, {0.1, 0.3 - 0.2i}},
	}
	controllerResponses := [][][]complex128{
		{{0.2, 0.03i}, {0.01, 0.15}},
		{{0.18, 0.02}, {-0.01i, 0.12}},
		{{0.1, 0.01i}, {0.02, 0.08}},
	}
	plant := identificationFRDModel(
		t, omega, 0, plantResponses, []string{"u1", "u2"}, []string{"y1", "y2"},
	)
	controller := identificationFRDModel(
		t, omega, 0, controllerResponses, []string{"y1", "y2"}, []string{"u1", "u2"},
	)
	workflow := NewIdentificationWorkflow()

	series, err := workflow.InterconnectFRD(FRDInterconnectionRequest{
		Operation: FRDOperationSeries, Left: plant, Right: &controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	seriesFRD, err := series.Model.controlsys()
	if err != nil {
		t.Fatal(err)
	}
	for frequency := range omega {
		want := multiply2x2(controllerResponses[frequency], plantResponses[frequency])
		assertComplexMatrixClose(t, seriesFRD.Response[frequency], want, 1e-13)
	}

	parallelRight := identificationFRDModel(
		t, omega, 0, controllerResponses, []string{"u1", "u2"}, []string{"y1", "y2"},
	)
	parallel, err := workflow.InterconnectFRD(FRDInterconnectionRequest{
		Operation: FRDOperationParallel, Left: plant, Right: &parallelRight,
	})
	if err != nil {
		t.Fatal(err)
	}
	parallelFRD, err := parallel.Model.controlsys()
	if err != nil {
		t.Fatal(err)
	}
	for frequency := range omega {
		want := add2x2(plantResponses[frequency], controllerResponses[frequency])
		assertComplexMatrixClose(t, parallelFRD.Response[frequency], want, 1e-13)
	}

	feedback, err := workflow.InterconnectFRD(FRDInterconnectionRequest{
		Operation: FRDOperationFeedback, Left: plant, Right: &controller, Sign: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	feedbackFRD, err := feedback.Model.controlsys()
	if err != nil {
		t.Fatal(err)
	}
	for frequency := range omega {
		kg := multiply2x2(controllerResponses[frequency], plantResponses[frequency])
		denominator := add2x2([][]complex128{{1, 0}, {0, 1}}, kg)
		want := multiply2x2(plantResponses[frequency], invert2x2(denominator))
		assertComplexMatrixClose(t, feedbackFRD.Response[frequency], want, 1e-12)
	}
}

func TestIdentificationWorkflowFRDRouteRequiresExactGridAndNames(t *testing.T) {
	base := identificationFRDModel(
		t, []float64{0.1, 1}, 0,
		[][][]complex128{{{1}}, {{0.5 - 0.5i}}},
		[]string{"u"}, []string{"y"},
	)
	imported, err := NewIdentificationWorkflow().ImportFRD(FRDImportRequest{
		Name: "bench response", Source: "bench-run-17.csv", Model: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	if imported.Provenance.Source != "bench-run-17.csv" ||
		imported.Fit != nil || imported.Diagnostics != nil {
		t.Fatalf("imported candidate provenance/evidence = %+v", imported)
	}
	base = imported.Model
	differentGrid := identificationFRDModel(
		t, []float64{0.1, math.Nextafter(1, 2)}, 0,
		[][][]complex128{{{1}}, {{0.5 - 0.5i}}},
		[]string{"u"}, []string{"y"},
	)
	_, err = NewIdentificationWorkflow().InterconnectFRD(FRDInterconnectionRequest{
		Operation: FRDOperationParallel, Left: base, Right: &differentGrid,
	})
	if err == nil || !strings.Contains(err.Error(), "frequency grids differ") {
		t.Fatalf("grid error = %v", err)
	}

	differentNames := identificationFRDModel(
		t, []float64{0.1, 1}, 0,
		[][][]complex128{{{1}}, {{0.5 - 0.5i}}},
		[]string{"disturbance"}, []string{"y"},
	)
	_, err = NewIdentificationWorkflow().InterconnectFRD(FRDInterconnectionRequest{
		Operation: FRDOperationParallel, Left: base, Right: &differentNames,
	})
	if err == nil || !strings.Contains(err.Error(), "parallel inputs differ") {
		t.Fatalf("name error = %v", err)
	}

	margin, err := NewIdentificationWorkflow().InterconnectFRD(FRDInterconnectionRequest{
		Operation: FRDOperationMargin, Left: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	if margin.Margin == nil || margin.Model != nil {
		t.Fatalf("margin result = %+v", margin)
	}
}

func identificationTestDataset(
	t *testing.T,
	inputs, outputs [][]float64,
	dt float64,
	inputNames, outputNames []string,
	split IdentificationSplit,
) IdentificationDataset {
	t.Helper()
	inputMatrix := rowMatrixValue(t, inputs)
	outputMatrix := rowMatrixValue(t, outputs)
	inputUnits := make([]string, len(inputs))
	for index := range inputUnits {
		inputUnits[index] = "input-unit"
	}
	outputUnits := make([]string, len(outputs))
	for index := range outputUnits {
		outputUnits[index] = "output-unit"
	}
	return IdentificationDataset{
		Inputs: inputMatrix, Outputs: outputMatrix,
		InputNames: inputNames, OutputNames: outputNames,
		InputUnits: inputUnits, OutputUnits: outputUnits,
		SampleTime: dt, TimeUnit: "s", Split: split,
		Preprocessing: PreprocessingRemoveMean,
	}
}

func rowMatrixValue(t *testing.T, rows [][]float64) MatrixValue {
	t.Helper()
	if len(rows) == 0 {
		t.Fatal("test matrix has no rows")
	}
	columns := len(rows[0])
	values := make([]float64, 0, len(rows)*columns)
	for _, row := range rows {
		if len(row) != columns {
			t.Fatal("test matrix rows differ")
		}
		values = append(values, row...)
	}
	value, err := NewMatrixValue(len(rows), columns, values)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func identificationFRDModel(
	t *testing.T,
	omega []float64,
	dt float64,
	response [][][]complex128,
	inputNames, outputNames []string,
) FRDModel {
	t.Helper()
	frd, err := controlsys.NewFRD(response, omega, dt)
	if err != nil {
		t.Fatal(err)
	}
	frd.InputName = append([]string(nil), inputNames...)
	frd.OutputName = append([]string(nil), outputNames...)
	inputUnits := make([]string, len(inputNames))
	for index, name := range inputNames {
		inputUnits[index] = name + "-unit"
	}
	outputUnits := make([]string, len(outputNames))
	for index, name := range outputNames {
		outputUnits[index] = name + "-unit"
	}
	model, err := newFRDModel(frd, "s", inputUnits, outputUnits)
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func median(values []float64) float64 {
	copied := append([]float64(nil), values...)
	for index := 1; index < len(copied); index++ {
		for position := index; position > 0 && copied[position] < copied[position-1]; position-- {
			copied[position], copied[position-1] = copied[position-1], copied[position]
		}
	}
	return copied[len(copied)/2]
}

func identificationEqualStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func multiply2x2(left, right [][]complex128) [][]complex128 {
	return [][]complex128{
		{
			left[0][0]*right[0][0] + left[0][1]*right[1][0],
			left[0][0]*right[0][1] + left[0][1]*right[1][1],
		},
		{
			left[1][0]*right[0][0] + left[1][1]*right[1][0],
			left[1][0]*right[0][1] + left[1][1]*right[1][1],
		},
	}
}

func add2x2(left, right [][]complex128) [][]complex128 {
	return [][]complex128{
		{left[0][0] + right[0][0], left[0][1] + right[0][1]},
		{left[1][0] + right[1][0], left[1][1] + right[1][1]},
	}
}

func invert2x2(value [][]complex128) [][]complex128 {
	determinant := value[0][0]*value[1][1] - value[0][1]*value[1][0]
	return [][]complex128{
		{value[1][1] / determinant, -value[0][1] / determinant},
		{-value[1][0] / determinant, value[0][0] / determinant},
	}
}

func assertComplexMatrixClose(
	t *testing.T,
	got, want [][]complex128,
	tolerance float64,
) {
	t.Helper()
	for row := range want {
		for column := range want[row] {
			if difference := cmplx.Abs(got[row][column] - want[row][column]); difference > tolerance {
				t.Fatalf(
					"matrix[%d][%d] = %v, want %v (difference %.3g)",
					row, column, got[row][column], want[row][column], difference,
				)
			}
		}
	}
}

func TestIdentificationWorkflowErrorsAreValidationErrors(t *testing.T) {
	_, err := NewIdentificationWorkflow().EstimateFrequencyResponse(
		FrequencyIdentificationRequest{},
	)
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %T %v, want ValidationError", err, err)
	}
}
