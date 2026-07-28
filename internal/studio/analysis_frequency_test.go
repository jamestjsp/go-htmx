package studio

import (
	"encoding/json"
	"math"
	"math/cmplx"
	"strings"
	"testing"
)

func TestFrequencyAnalysisMatchesFirstOrderComplexOracle(t *testing.T) {
	omega := []float64{0.1, 1, 10}
	result, err := analyzeFrequency(
		analysisSISOBlocks([]float64{1}, []float64{1, 1}),
		[]Connection{{SourceID: 1, TargetID: 2}},
		FrequencyAnalysisRequest{
			Inputs:  []ChannelRef{{BlockID: 1}},
			Outputs: []ChannelRef{{BlockID: 2}},
			Omega:   omega,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "siso" || result.Grid.Source != "explicit" ||
		result.Units.Frequency != "rad/s" ||
		result.Units.Magnitude != "dB" ||
		result.Units.Phase != "degrees" {
		t.Fatalf("frequency metadata = %#v", result)
	}
	if len(result.Bode) != 1 || result.Nyquist == nil || result.Nichols == nil {
		t.Fatalf("SISO frequency result = %#v", result)
	}
	for i, frequency := range omega {
		wantMagnitude := -10 * math.Log10(1+frequency*frequency)
		wantPhase := -math.Atan(frequency) * 180 / math.Pi
		if result.Bode[0].MagnitudeDB[i] == nil ||
			math.Abs(*result.Bode[0].MagnitudeDB[i]-wantMagnitude) > 1e-10 {
			t.Fatalf("magnitude[%d] = %v, want %g", i, result.Bode[0].MagnitudeDB[i], wantMagnitude)
		}
		if result.Bode[0].PhaseDegrees[i] == nil ||
			math.Abs(*result.Bode[0].PhaseDegrees[i]-wantPhase) > 1e-10 {
			t.Fatalf("phase[%d] = %v, want %g", i, result.Bode[0].PhaseDegrees[i], wantPhase)
		}
		if result.Nichols.MagnitudeDB[i] == nil ||
			math.Abs(*result.Nichols.MagnitudeDB[i]-wantMagnitude) > 1e-10 {
			t.Fatalf("Nichols magnitude[%d] = %v, want %g", i, result.Nichols.MagnitudeDB[i], wantMagnitude)
		}
	}
	if result.SingularValues == nil ||
		len(result.SingularValues.Values) != 1 {
		t.Fatalf("singular values = %#v", result.SingularValues)
	}
}

func TestFrequencyAnalysisBuildsAutomaticGrid(t *testing.T) {
	result, err := analyzeFrequency(
		analysisSISOBlocks([]float64{1}, []float64{1, 1}),
		[]Connection{{SourceID: 1, TargetID: 2}},
		FrequencyAnalysisRequest{
			Inputs:  []ChannelRef{{BlockID: 1}},
			Outputs: []ChannelRef{{BlockID: 2}},
			Points:  50,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Grid.Source != "automatic" || len(result.Grid.Omega) != 50 {
		t.Fatalf("automatic grid = %#v", result.Grid)
	}
	if result.Grid.Omega[0] >= 1 || result.Grid.Omega[len(result.Grid.Omega)-1] <= 1 {
		t.Fatalf("automatic grid does not span the pole: %v", result.Grid.Omega)
	}
}

func TestFrequencyAnalysisReturnsMIMOSingularValues(t *testing.T) {
	source := defaultParameters(BlockVectorConstant)
	gain := defaultParameters(BlockMatrixGain)
	matrix, err := NewMatrixValue(2, 2, []float64{3, 0, 0, 2})
	if err != nil {
		t.Fatal(err)
	}
	gain.D = &matrix
	result, err := analyzeFrequency(
		[]Block{
			{ID: 1, Kind: BlockVectorConstant, Name: "Input", Parameters: source},
			{ID: 2, Kind: BlockMatrixGain, Name: "Plant", Parameters: gain},
		},
		[]Connection{{SourceID: 1, TargetID: 2}},
		FrequencyAnalysisRequest{
			Inputs: []ChannelRef{
				{BlockID: 1, Channel: 0},
				{BlockID: 1, Channel: 1},
			},
			Outputs: []ChannelRef{
				{BlockID: 2, Channel: 0},
				{BlockID: 2, Channel: 1},
			},
			Omega: []float64{0.1, 1},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "mimo" || result.Nyquist != nil || result.Nichols != nil {
		t.Fatalf("MIMO applicability = %#v", result)
	}
	if result.SingularValues == nil ||
		len(result.SingularValues.Values) != 2 {
		t.Fatalf("singular values = %#v", result.SingularValues)
	}
	for sample := range 2 {
		if got := result.SingularValues.Values[0][sample]; got == nil || math.Abs(*got-3) > 1e-12 {
			t.Fatalf("sigma 1 at %d = %v, want 3", sample, got)
		}
		if got := result.SingularValues.Values[1][sample]; got == nil || math.Abs(*got-2) > 1e-12 {
			t.Fatalf("sigma 2 at %d = %v, want 2", sample, got)
		}
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatalf("marshal MIMO frequency result with zero channels: %v", err)
	}
}

func TestFrequencyAnalysisRefusesAboveDiscreteNyquist(t *testing.T) {
	_, err := analyzeFrequency(
		[]Block{
			{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
			{ID: 2, Kind: BlockUnitDelay, Name: "Memory", Parameters: Parameters{
				SampleTime: 0.1, SampleTimeMode: string(sampleTimeExplicit),
			}},
		},
		[]Connection{{SourceID: 1, TargetID: 2}},
		FrequencyAnalysisRequest{
			Inputs:   []ChannelRef{{BlockID: 1}},
			Outputs:  []ChannelRef{{BlockID: 2}},
			BaseStep: 0.1,
			Omega:    []float64{1, 32},
		},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "32 rad/s") ||
		!strings.Contains(err.Error(), "Nyquist limit") ||
		!strings.Contains(err.Error(), "0.1 s") {
		t.Fatalf("error = %v, want discrete Nyquist context", err)
	}
}

func TestFrequencyAnalysisCapsAutomaticDiscreteGridAtNyquist(t *testing.T) {
	result, err := analyzeFrequency(
		[]Block{
			{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
			{ID: 2, Kind: BlockUnitDelay, Name: "Memory", Parameters: Parameters{
				SampleTime: 0.1, SampleTimeMode: string(sampleTimeExplicit),
			}},
		},
		[]Connection{{SourceID: 1, TargetID: 2}},
		FrequencyAnalysisRequest{
			Inputs:   []ChannelRef{{BlockID: 1}},
			Outputs:  []ChannelRef{{BlockID: 2}},
			BaseStep: 0.1,
			Points:   25,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantNyquist := math.Pi / 0.1
	if result.Grid.DiscreteNyquist == nil ||
		math.Abs(*result.Grid.DiscreteNyquist-wantNyquist) > 1e-12 ||
		len(result.Grid.Omega) != 25 ||
		math.Abs(result.Grid.Omega[len(result.Grid.Omega)-1]-wantNyquist) > 1e-12 {
		t.Fatalf("discrete grid = %#v", result.Grid)
	}
}

func TestFrequencyAnalysisValidatesExplicitGrid(t *testing.T) {
	request := FrequencyAnalysisRequest{
		Inputs:  []ChannelRef{{BlockID: 1}},
		Outputs: []ChannelRef{{BlockID: 2}},
		Omega:   []float64{1, 0.5},
	}
	if err := validateFrequencyRequest(request); err == nil ||
		!strings.Contains(err.Error(), "strictly increasing") {
		t.Fatalf("error = %v, want increasing-grid context", err)
	}
}

func TestFrequencyAnalysisPreservesInternalDelayInNamedSelection(t *testing.T) {
	omega := []float64{1, 2}
	result, err := analyzeFrequency(
		[]Block{
			{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
			{ID: 2, Kind: BlockSum, Name: "Error", Parameters: Parameters{Signs: "+-"}},
			{ID: 3, Kind: BlockLag, Name: "Plant", Parameters: Parameters{TimeConstant: 1}},
			{ID: 4, Kind: BlockDelay, Name: "Feedback delay", Parameters: Parameters{
				Delay: 0.2, DelayMode: delayModeExact,
			}},
		},
		[]Connection{
			{SourceID: 1, TargetID: 2, TargetPort: 0},
			{SourceID: 4, TargetID: 2, TargetPort: 1},
			{SourceID: 2, TargetID: 3},
			{SourceID: 3, TargetID: 4},
		},
		FrequencyAnalysisRequest{
			Inputs:  []ChannelRef{{BlockID: 1}},
			Outputs: []ChannelRef{{BlockID: 4}},
			Omega:   omega,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for i, frequency := range omega {
		plant := 1 / complex(1, frequency)
		delay := cmplx.Exp(complex(0, -0.2*frequency))
		want := delay * plant / (1 + delay*plant)
		got := complex(
			*result.Nyquist.Positive[i].Real,
			*result.Nyquist.Positive[i].Imag,
		)
		if cmplx.Abs(got-want) > 1e-10 {
			t.Fatalf("response[%d] = %v, want exact delayed loop %v", i, got, want)
		}
	}
}

func TestFrequencyAnalysisPreservesExternalPureDelayInNamedSelection(t *testing.T) {
	omega := []float64{1, 2}
	result, err := analyzeFrequency(
		[]Block{
			{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
			{ID: 2, Kind: BlockDelay, Name: "Transport", Parameters: Parameters{
				Delay: 0.3, DelayMode: delayModeExact,
			}},
		},
		[]Connection{{SourceID: 1, TargetID: 2}},
		FrequencyAnalysisRequest{
			Inputs:  []ChannelRef{{BlockID: 1}},
			Outputs: []ChannelRef{{BlockID: 2}},
			Omega:   omega,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for i, frequency := range omega {
		want := cmplx.Exp(complex(0, -0.3*frequency))
		got := complex(
			*result.Nyquist.Positive[i].Real,
			*result.Nyquist.Positive[i].Imag,
		)
		if cmplx.Abs(got-want) > 1e-12 {
			t.Fatalf("response[%d] = %v, want pure delay %v", i, got, want)
		}
	}
}
