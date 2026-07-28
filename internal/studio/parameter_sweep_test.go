package studio

import (
	"fmt"
	"math"
	"math/cmplx"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestParameterSweepTwoAxisOrderingAndAnalyticResponses(t *testing.T) {
	revision := time.Date(2026, 7, 28, 9, 30, 0, 0, time.UTC)
	var compiledNames []string
	source := analyticSweepSource(revision, &compiledNames)
	spec := SweepSpec{
		SourceModelRevision: revision,
		Axes: []SweepAxis{
			{Parameter: "gain", Unit: "1", Values: []float64{1, 3}},
			{Parameter: "time_constant", Unit: "s", Values: []float64{0.5, 2}},
		},
	}

	sweep, err := MaterializeParameterSweep(source, spec)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sweep.Shape, []int{2, 2}; !slices.Equal(got, want) {
		t.Fatalf("shape = %v, want %v", got, want)
	}
	wantCoordinates := [][]int{{0, 0}, {0, 1}, {1, 0}, {1, 1}}
	wantValues := [][]float64{{1, 0.5}, {1, 2}, {3, 0.5}, {3, 2}}
	wantNames := []string{
		"lag [gain=1 1, time_constant=0.5 s]",
		"lag [gain=1 1, time_constant=2 s]",
		"lag [gain=3 1, time_constant=0.5 s]",
		"lag [gain=3 1, time_constant=2 s]",
	}
	for modelIndex, variant := range sweep.Variants {
		if !slices.Equal(variant.Coordinates, wantCoordinates[modelIndex]) ||
			!slices.Equal(variant.Values, wantValues[modelIndex]) {
			t.Fatalf(
				"variant %d coordinates/values = %v/%v, want %v/%v",
				modelIndex, variant.Coordinates, variant.Values,
				wantCoordinates[modelIndex], wantValues[modelIndex],
			)
		}
		if variant.Name != wantNames[modelIndex] {
			t.Fatalf("variant %d name = %q, want %q", modelIndex, variant.Name, wantNames[modelIndex])
		}
	}
	if !slices.Equal(compiledNames, wantNames) {
		t.Fatalf("compile order = %v, want %v", compiledNames, wantNames)
	}
	if source.Parameters.Gain != 0.25 || source.Parameters.TimeConstant != 0.25 ||
		source.Parameters.Numerator[0] != 7 {
		t.Fatal("materialization mutated the authored source parameters")
	}
	sweep.Variants[0].Parameters.Numerator[0] = 99
	if source.Parameters.Numerator[0] != 7 {
		t.Fatal("retained variant parameters alias the authored source")
	}

	analysis, err := AnalyzeParameterSweep(sweep, SweepAnalysisSpec{
		Omega: []float64{0.25, 1, 4}, StepFinal: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	for modelIndex, values := range wantValues {
		gain, timeConstant := values[0], values[1]
		frequencyResponse := analysis.Frequency.Responses.Responses[modelIndex]
		for frequencyIndex, frequency := range frequencyResponse.Omega {
			want := complex(gain, 0) / complex(1, frequency*timeConstant)
			if difference := cmplx.Abs(frequencyResponse.At(frequencyIndex, 0, 0) - want); difference > 1e-11 {
				t.Fatalf(
					"model %d H(j%g) differs by %g: got %v want %v",
					modelIndex, frequency, difference,
					frequencyResponse.At(frequencyIndex, 0, 0), want,
				)
			}
		}
		step := analysis.Time.Responses.Responses[modelIndex]
		system, ok, err := sweep.Models.ModelFlat(modelIndex)
		if err != nil || !ok {
			t.Fatalf("model %d unavailable: ok=%v err=%v", modelIndex, ok, err)
		}
		estimatedSamples, err := parameterSweepStepSamples(system, 2)
		if err != nil {
			t.Fatal(err)
		}
		if estimatedSamples != len(step.T) {
			t.Fatalf(
				"model %d bounded samples = %d, actual = %d",
				modelIndex, estimatedSamples, len(step.T),
			)
		}
		for sample, currentTime := range step.T {
			want := gain * (1 - math.Exp(-currentTime/timeConstant))
			if difference := math.Abs(step.Y.At(0, sample) - want); difference > 1e-11 {
				t.Fatalf(
					"model %d step(%g) differs by %g: got %g want %g",
					modelIndex, currentTime, difference, step.Y.At(0, sample), want,
				)
			}
		}
	}
	if analysis.Frequency.WorstCase.FlatIndex != 2 ||
		analysis.Frequency.WorstCase.Name != wantNames[2] ||
		analysis.Frequency.WorstCase.PeakOmega != 0.25 {
		t.Fatalf("frequency worst case = %#v, want model 2 at 0.25 rad/s", analysis.Frequency.WorstCase)
	}
	if analysis.Time.WorstCase.FlatIndex != 2 ||
		analysis.Time.WorstCase.Name != wantNames[2] {
		t.Fatalf("time worst case = %#v, want model 2", analysis.Time.WorstCase)
	}
}

func TestParameterSweepMIMOStaticWorstCaseUsesLargestSingularValue(t *testing.T) {
	revision := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	source := SweepModelSource{
		Name: "static-mimo", ModelRevision: revision,
		Parameters: Parameters{Gain: 1},
		SetParameter: func(parameters *Parameters, name string, value float64) error {
			if name != "gain_11" {
				return fmt.Errorf("unknown parameter %q", name)
			}
			parameters.Gain = value
			return nil
		},
		Compile: func(name string, parameters Parameters) (*controlsys.System, error) {
			system, err := controlsys.NewGain(
				mat.NewDense(2, 2, []float64{parameters.Gain, 0, 0, 2}),
				0,
			)
			if err != nil {
				return nil, err
			}
			system.InputName = []string{"feed", "utility"}
			system.OutputName = []string{"product", "energy"}
			system.Notes = name
			return system, nil
		},
	}
	sweep, err := MaterializeParameterSweep(source, SweepSpec{
		SourceModelRevision: revision,
		Axes: []SweepAxis{{
			Parameter: "gain_11", Unit: "1", Values: []float64{1, 5},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := AnalyzeParameterSweep(sweep, SweepAnalysisSpec{
		Omega: []float64{1, 2}, StepFinal: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := analysis.Frequency.Models[0].PeakGain; math.Abs(got-2) > 1e-12 {
		t.Fatalf("first model peak sigma = %g, want 2", got)
	}
	if got := analysis.Frequency.Models[1].PeakGain; math.Abs(got-5) > 1e-12 {
		t.Fatalf("second model peak sigma = %g, want 5", got)
	}
	if analysis.Frequency.WorstCase.FlatIndex != 1 ||
		analysis.Frequency.WorstCase.PeakGain != 5 {
		t.Fatalf("MIMO worst case = %#v, want model 1 at sigma 5", analysis.Frequency.WorstCase)
	}
}

func TestParameterSweepRefusesIncompatibleCompiledFamilies(t *testing.T) {
	revision := time.Date(2026, 7, 28, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name    string
		compile func(float64) (*controlsys.System, error)
		want    string
	}{
		{
			name: "dimensions",
			compile: func(value float64) (*controlsys.System, error) {
				if value == 1 {
					return namedStaticSweepSystem([]float64{1}, 1, 1, 0, nil)
				}
				return namedStaticSweepSystem([]float64{1, 2}, 2, 1, 0, nil)
			},
			want: "dimensions",
		},
		{
			name: "time domain",
			compile: func(value float64) (*controlsys.System, error) {
				dt := 0.0
				if value == 2 {
					dt = 0.1
				}
				return namedStaticSweepSystem([]float64{1}, 1, 1, dt, nil)
			},
			want: "time domain/sample time",
		},
		{
			name: "sample time",
			compile: func(value float64) (*controlsys.System, error) {
				dt := 0.1
				if value == 2 {
					dt = 0.2
				}
				return namedStaticSweepSystem([]float64{1}, 1, 1, dt, nil)
			},
			want: "time domain/sample time",
		},
		{
			name: "channel names",
			compile: func(value float64) (*controlsys.System, error) {
				inputName := "command"
				if value == 2 {
					inputName = "disturbance"
				}
				return namedStaticSweepSystem(
					[]float64{1}, 1, 1, 0,
					[]string{inputName, "response"},
				)
			},
			want: "names differ",
		},
		{
			name: "incomplete names",
			compile: func(float64) (*controlsys.System, error) {
				system, err := namedStaticSweepSystem([]float64{1}, 1, 1, 0, nil)
				if err != nil {
					return nil, err
				}
				system.InputName = nil
				return system, nil
			},
			want: "complete input names",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := scalarSweepSource(revision, test.compile)
			_, err := MaterializeParameterSweep(source, SweepSpec{
				SourceModelRevision: revision,
				Axes: []SweepAxis{{
					Parameter: "variant", Unit: "1", Values: []float64{1, 2},
				}},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want text %q", err, test.want)
			}
			if strings.Contains(err.Error(), "NewModelArray") {
				t.Fatalf("compatibility reached ModelArray instead of preflight: %v", err)
			}
		})
	}
}

func TestParameterSweepRefusesWorkBeyondWebBounds(t *testing.T) {
	revision := time.Date(2026, 7, 28, 11, 0, 0, 0, time.UTC)
	values := make([]float64, maxParameterSweepModels+1)
	for index := range values {
		values[index] = float64(index + 1)
	}
	source := scalarSweepSource(revision, func(value float64) (*controlsys.System, error) {
		return namedStaticSweepSystem([]float64{value}, 1, 1, 0, nil)
	})
	if _, err := MaterializeParameterSweep(source, SweepSpec{
		SourceModelRevision: revision,
		Axes:                []SweepAxis{{Parameter: "variant", Unit: "1", Values: values}},
	}); err == nil || !strings.Contains(err.Error(), "limited to 64 models") {
		t.Fatalf("model bound error = %v", err)
	}
	oversizedStateSource := scalarSweepSource(revision, func(float64) (*controlsys.System, error) {
		states := maxParameterSweepStates + 1
		system, err := controlsys.New(
			mat.NewDense(states, states, nil),
			mat.NewDense(states, 1, nil),
			mat.NewDense(1, states, nil),
			mat.NewDense(1, 1, nil),
			0,
		)
		if err != nil {
			return nil, err
		}
		system.InputName = []string{"command"}
		system.OutputName = []string{"response"}
		system.StateName = make([]string, states)
		for state := range states {
			system.StateName[state] = fmt.Sprintf("state_%d", state+1)
		}
		return system, nil
	})
	if _, err := MaterializeParameterSweep(oversizedStateSource, SweepSpec{
		SourceModelRevision: revision,
		Axes:                []SweepAxis{{Parameter: "variant", Unit: "1", Values: []float64{1}}},
	}); err == nil || !strings.Contains(err.Error(), "limited to 64 states") {
		t.Fatalf("state bound error = %v", err)
	}

	continuous, err := MaterializeParameterSweep(
		scalarSweepSource(revision, func(value float64) (*controlsys.System, error) {
			return namedStaticSweepSystem([]float64{value}, 1, 1, 0, nil)
		}),
		SweepSpec{
			SourceModelRevision: revision,
			Axes:                []SweepAxis{{Parameter: "variant", Unit: "1", Values: []float64{1}}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	omega := make([]float64, maxParameterSweepFrequencies+1)
	for index := range omega {
		omega[index] = float64(index + 1)
	}
	if _, err := AnalyzeParameterSweep(continuous, SweepAnalysisSpec{
		Omega: omega, StepFinal: 1,
	}); err == nil || !strings.Contains(err.Error(), "between 2 and 256 points") {
		t.Fatalf("frequency bound error = %v", err)
	}

	discrete, err := MaterializeParameterSweep(
		scalarSweepSource(revision, func(value float64) (*controlsys.System, error) {
			return namedStaticSweepSystem([]float64{value}, 1, 1, 0.001, nil)
		}),
		SweepSpec{
			SourceModelRevision: revision,
			Axes:                []SweepAxis{{Parameter: "variant", Unit: "1", Values: []float64{1}}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AnalyzeParameterSweep(discrete, SweepAnalysisSpec{
		Omega: []float64{1, 2}, StepFinal: 3,
	}); err == nil || !strings.Contains(err.Error(), "limited to 2000 samples") {
		t.Fatalf("time bound error = %v", err)
	}
}

func TestParameterSweepRequiresExactRevisionAndExplicitAxisMetadata(t *testing.T) {
	revision := time.Date(2026, 7, 28, 11, 30, 0, 0, time.UTC)
	source := scalarSweepSource(revision, func(value float64) (*controlsys.System, error) {
		return namedStaticSweepSystem([]float64{value}, 1, 1, 0, nil)
	})
	tests := []struct {
		name string
		spec SweepSpec
		want string
	}{
		{
			name: "stale revision",
			spec: SweepSpec{
				SourceModelRevision: revision.Add(time.Second),
				Axes:                []SweepAxis{{Parameter: "gain", Unit: "1", Values: []float64{1}}},
			},
			want: "does not match",
		},
		{
			name: "missing unit",
			spec: SweepSpec{
				SourceModelRevision: revision,
				Axes:                []SweepAxis{{Parameter: "gain", Values: []float64{1}}},
			},
			want: "explicit unit",
		},
		{
			name: "nonfinite value",
			spec: SweepSpec{
				SourceModelRevision: revision,
				Axes:                []SweepAxis{{Parameter: "gain", Unit: "1", Values: []float64{math.Inf(1)}}},
			},
			want: "must be finite",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := MaterializeParameterSweep(source, test.spec); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want text %q", err, test.want)
			}
		})
	}
}

func BenchmarkParameterSweepAnalysisScaling(b *testing.B) {
	cases := []struct {
		models      int
		states      int
		frequencies int
	}{
		{models: 8, states: 2, frequencies: 16},
		{models: 32, states: 2, frequencies: 64},
		{models: 16, states: 8, frequencies: 64},
	}
	for _, test := range cases {
		name := fmt.Sprintf(
			"models=%d/states=%d/frequencies=%d",
			test.models, test.states, test.frequencies,
		)
		b.Run(name, func(b *testing.B) {
			sweep := benchmarkParameterSweep(b, test.models, test.states)
			omega := make([]float64, test.frequencies)
			for index := range omega {
				omega[index] = 0.1 + float64(index)*0.2
			}
			spec := SweepAnalysisSpec{Omega: omega, StepFinal: 0.5}
			b.ReportMetric(float64(test.models), "models")
			b.ReportMetric(float64(test.states), "states/model")
			b.ReportMetric(float64(test.frequencies), "frequencies")
			b.ResetTimer()
			for range b.N {
				if _, err := AnalyzeParameterSweep(sweep, spec); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func analyticSweepSource(revision time.Time, compiledNames *[]string) SweepModelSource {
	return SweepModelSource{
		Name: "lag", ModelRevision: revision,
		Parameters: Parameters{
			Gain: 0.25, TimeConstant: 0.25, Numerator: []float64{7, 8},
		},
		SetParameter: func(parameters *Parameters, name string, value float64) error {
			switch name {
			case "gain":
				parameters.Gain = value
			case "time_constant":
				parameters.TimeConstant = value
			default:
				return fmt.Errorf("unknown parameter %q", name)
			}
			return nil
		},
		Compile: func(name string, parameters Parameters) (*controlsys.System, error) {
			*compiledNames = append(*compiledNames, name)
			system, err := controlsys.New(
				mat.NewDense(1, 1, []float64{-1 / parameters.TimeConstant}),
				mat.NewDense(1, 1, []float64{parameters.Gain / parameters.TimeConstant}),
				mat.NewDense(1, 1, []float64{1}),
				mat.NewDense(1, 1, nil),
				0,
			)
			if err != nil {
				return nil, err
			}
			system.InputName = []string{"command"}
			system.OutputName = []string{"response"}
			system.StateName = []string{"lag_state"}
			system.Notes = name
			return system, nil
		},
	}
}

func scalarSweepSource(
	revision time.Time,
	compile func(float64) (*controlsys.System, error),
) SweepModelSource {
	return SweepModelSource{
		Name: "family", ModelRevision: revision,
		SetParameter: func(parameters *Parameters, name string, value float64) error {
			if name != "variant" {
				return fmt.Errorf("unknown parameter %q", name)
			}
			parameters.Gain = value
			return nil
		},
		Compile: func(_ string, parameters Parameters) (*controlsys.System, error) {
			return compile(parameters.Gain)
		},
	}
}

func namedStaticSweepSystem(
	values []float64,
	outputs, inputs int,
	dt float64,
	names []string,
) (*controlsys.System, error) {
	system, err := controlsys.NewGain(mat.NewDense(outputs, inputs, values), dt)
	if err != nil {
		return nil, err
	}
	inputName, outputName := "command", "response"
	if len(names) == 2 {
		inputName, outputName = names[0], names[1]
	}
	system.InputName = make([]string, inputs)
	for index := range inputs {
		system.InputName[index] = inputName
		if inputs > 1 {
			system.InputName[index] += fmt.Sprintf("_%d", index+1)
		}
	}
	system.OutputName = make([]string, outputs)
	for index := range outputs {
		system.OutputName[index] = outputName
		if outputs > 1 {
			system.OutputName[index] += fmt.Sprintf("_%d", index+1)
		}
	}
	return system, nil
}

func benchmarkParameterSweep(b *testing.B, modelCount, states int) *ParameterSweep {
	b.Helper()
	revision := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	values := make([]float64, modelCount)
	for index := range values {
		values[index] = 0.5 + float64(index)/float64(modelCount)
	}
	source := SweepModelSource{
		Name: "benchmark", ModelRevision: revision,
		SetParameter: func(parameters *Parameters, name string, value float64) error {
			if name != "gain" {
				return fmt.Errorf("unknown parameter %q", name)
			}
			parameters.Gain = value
			return nil
		},
		Compile: func(name string, parameters Parameters) (*controlsys.System, error) {
			a := mat.NewDense(states, states, nil)
			bMatrix := mat.NewDense(states, 1, nil)
			c := mat.NewDense(1, states, nil)
			for state := range states {
				a.Set(state, state, -float64(state+1))
				bMatrix.Set(state, 0, parameters.Gain/float64(states))
				c.Set(0, state, 1)
			}
			system, err := controlsys.New(
				a, bMatrix, c, mat.NewDense(1, 1, nil), 0,
			)
			if err != nil {
				return nil, err
			}
			system.InputName = []string{"command"}
			system.OutputName = []string{"response"}
			system.StateName = make([]string, states)
			for state := range states {
				system.StateName[state] = fmt.Sprintf("state_%d", state+1)
			}
			system.Notes = name
			return system, nil
		},
	}
	sweep, err := MaterializeParameterSweep(source, SweepSpec{
		SourceModelRevision: revision,
		Axes:                []SweepAxis{{Parameter: "gain", Unit: "1", Values: values}},
	})
	if err != nil {
		b.Fatal(err)
	}
	return sweep
}
