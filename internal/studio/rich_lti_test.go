package studio

import (
	"encoding/json"
	"math"
	"math/cmplx"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestRichLTIRepresentationsMatchIndependentMIMOOracle(t *testing.T) {
	stateParameters, transferParameters, zpkParameters := matchingMIMOParameters(t)
	cases := []struct {
		name       string
		parameters Parameters
		build      func(Parameters) (*controlsys.System, error)
	}{
		{name: "state-space", parameters: stateParameters, build: stateSpaceFromParameters},
		{name: "transfer", parameters: transferParameters, build: transferSystemFromParameters},
		{name: "zpk", parameters: zpkParameters, build: zpkSystemFromParameters},
	}
	omega := []float64{0.1, 1, 7}
	times := []float64{0, 0.1, 0.2, 0.3, 0.4}
	inputData := make([]float64, len(times)*2)
	for sample := range times {
		inputData[sample*2] = 1
		inputData[sample*2+1] = 0.5
	}
	input := mat.NewDense(len(times), 2, inputData)

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			system, err := test.build(test.parameters)
			if err != nil {
				t.Fatal(err)
			}
			if system.InputName[0] != "feed" || system.InputName[1] != "recycle" ||
				system.OutputName[0] != "temperature" || system.OutputName[1] != "pressure" {
				t.Fatalf("channel names = %v -> %v", system.InputName, system.OutputName)
			}
			response, err := system.FreqResponse(omega)
			if err != nil {
				t.Fatal(err)
			}
			for sample, frequency := range omega {
				for output := range 2 {
					rate := float64(output + 1)
					for inputChannel := range 2 {
						gain := [][]float64{{1, 2}, {3, 4}}[output][inputChannel]
						want := complex(gain, 0) / complex(rate, frequency)
						if difference := cmplx.Abs(response.At(sample, output, inputChannel) - want); difference > 1e-10 {
							t.Fatalf(
								"H[%d,%d](%g) differs by %g: got %v want %v",
								output, inputChannel, frequency, difference,
								response.At(sample, output, inputChannel), want,
							)
						}
					}
				}
			}

			simulation, err := controlsys.Lsim(system, input, times, nil)
			if err != nil {
				t.Fatal(err)
			}
			for sample, currentTime := range times {
				wantTemperature := 2 * (1 - math.Exp(-currentTime))
				wantPressure := 2.5 * (1 - math.Exp(-2*currentTime))
				if difference := math.Abs(simulation.Y.At(0, sample) - wantTemperature); difference > 1e-10 {
					t.Fatalf("temperature[%d] differs by %g", sample, difference)
				}
				if difference := math.Abs(simulation.Y.At(1, sample) - wantPressure); difference > 1e-10 {
					t.Fatalf("pressure[%d] differs by %g", sample, difference)
				}
			}
		})
	}
}

func TestFRDBlockPreservesIndependentComplexSamplesAndRefusesTimeRealization(t *testing.T) {
	omega := []float64{0.1, 1, 7}
	values := make([]complex128, 0, len(omega)*4)
	for _, frequency := range omega {
		for output := range 2 {
			rate := float64(output + 1)
			for input := range 2 {
				gain := [][]float64{{1, 2}, {3, 4}}[output][input]
				values = append(values, complex(gain, 0)/complex(rate, frequency))
			}
		}
	}
	frequencies, _ := NewVectorValue(omega)
	response, _ := NewComplexResponseValue(len(omega), 2, 2, values)
	parameters := defaultFRDParameters()
	parameters.Frequencies = &frequencies
	parameters.FrequencyResponse = &response

	if err := validateFRDParameters(parameters); err != nil {
		t.Fatal(err)
	}
	frd, err := frdFromParameters(parameters)
	if err != nil {
		t.Fatal(err)
	}
	for sample := range omega {
		for output := range 2 {
			for input := range 2 {
				if got := frd.Response[sample][output][input]; got != values[sample*4+output*2+input] {
					t.Fatalf("response[%d][%d][%d] = %v", sample, output, input, got)
				}
			}
		}
	}
	_, err = realizeBlock(
		Block{Kind: BlockFRD, Name: "Imported sweep", Parameters: parameters},
		[]int{0},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "realize Imported sweep") ||
		!strings.Contains(err.Error(), "frequency-domain data only") {
		t.Fatalf("FRD time realization error = %v", err)
	}
}

func TestRichLTIBlocksCarryExplicitContinuousAndDiscreteMetadata(t *testing.T) {
	parameters := defaultStateSpaceParameters()
	if domain := representationTimeDomain(parameters); domain.kind != timeDomainContinuous {
		t.Fatalf("default domain = %q", domain.kind)
	}
	continuous, err := stateSpaceFromParameters(parameters)
	if err != nil {
		t.Fatal(err)
	}
	if !continuous.IsContinuous() {
		t.Fatalf("default State-Space Dt = %g", continuous.Dt)
	}

	parameters.TimeDomain = modelDomainDiscrete
	parameters.SampleTimeMode = string(sampleTimeExplicit)
	parameters.SampleTime = 0.2
	if err := validateStateSpaceParameters(parameters); err != nil {
		t.Fatal(err)
	}
	discrete, err := stateSpaceFromParameters(parameters)
	if err != nil {
		t.Fatal(err)
	}
	if !discrete.IsDiscrete() || discrete.Dt != 0.2 {
		t.Fatalf("discrete State-Space Dt = %g", discrete.Dt)
	}

	transfer := defaultMIMOTransferParameters()
	transfer.TimeDomain = modelDomainDiscrete
	transfer.SampleTime = 0.1
	delay, _ := NewMatrixValue(2, 2, []float64{1, 0, 0, 2})
	transfer.TransferDelays = &delay
	if err := validateMIMOTransferParameters(transfer); err != nil {
		t.Fatal(err)
	}
	function := transferFunctionFromParameters(transfer)
	if function.Delay[0][0] != 1 || function.Delay[1][1] != 2 {
		t.Fatalf("pairwise transfer-function delay = %v", function.Delay)
	}
	system, err := transferSystemFromParameters(transfer)
	if err != nil {
		t.Fatal(err)
	}
	if !system.HasInternalDelay() {
		t.Fatal("delayed MIMO transfer was not prepared for safe interconnection")
	}
}

func TestRichLTIConversionErrorsKeepControlsysContextAndBlockIdentity(t *testing.T) {
	parameters := defaultZPKParameters()
	poles, err := NewComplexRootMatrixValue([][][]complex128{
		{{complex(-1, 2)}, {complex(-1, 2)}},
		{{-2}, {-2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parameters.Poles = &poles
	_, err = realizeBlock(
		Block{Kind: BlockZPK, Name: "Reactor ZPK", Parameters: parameters},
		[]int{0},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "realize Reactor ZPK") ||
		!strings.Contains(err.Error(), "controlsys ZPK conversion") {
		t.Fatalf("conversion error = %v", err)
	}
}

func TestRichParameterValuesRoundTripAndOwnNestedStorage(t *testing.T) {
	polynomials, err := ParsePolynomialMatrixValue("1, 2 | 0\n3 | 1, 4")
	if err != nil {
		t.Fatal(err)
	}
	if got := polynomials.Text(); got != "1, 2 | 0\n3 | 1, 4" {
		t.Fatalf("polynomial text = %q", got)
	}
	roots, err := ParseComplexRootMatrixValue("-1+2i, -1-2i | -\n0 | -3")
	if err != nil {
		t.Fatal(err)
	}
	if got := roots.Text(); got != "-1+2i, -1-2i | -\n0 | -3" {
		t.Fatalf("root text = %q", got)
	}
	response, err := ParseComplexResponseValue("1+2i | 0 | 0 | 3-4i\n2 | 1 | -1 | 4", 2, 2)
	if err != nil {
		t.Fatal(err)
	}

	parameters := Parameters{
		TransferNumerators: &polynomials,
		Zeros:              &roots,
		FrequencyResponse:  &response,
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Parameters
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TransferNumerators.Text() != polynomials.Text() ||
		decoded.Zeros.Text() != roots.Text() ||
		decoded.FrequencyResponse.Text() != response.Text() {
		t.Fatalf("round trip = %#v", decoded)
	}

	copied := polynomials.Values()
	copied[0][0][0] = 999
	if polynomials.Values()[0][0][0] != 1 {
		t.Fatal("polynomial storage aliases caller")
	}
	responseCopy := response.Values()
	responseCopy[0] = 999
	if response.Values()[0] != complex(1, 2) {
		t.Fatal("frequency response storage aliases caller")
	}
}

func matchingMIMOParameters(t *testing.T) (Parameters, Parameters, Parameters) {
	t.Helper()
	inputs, _ := NewChannelNames([]string{"feed", "recycle"})
	outputs, _ := NewChannelNames([]string{"temperature", "pressure"})
	states, _ := NewChannelNames([]string{"temperature state", "pressure state"})
	a, _ := NewMatrixValue(2, 2, []float64{-1, 0, 0, -2})
	b, _ := NewMatrixValue(2, 2, []float64{1, 2, 3, 4})
	c, _ := NewMatrixValue(2, 2, []float64{1, 0, 0, 1})
	d, _ := NewMatrixValue(2, 2, []float64{0, 0, 0, 0})
	stateParameters := Parameters{
		TimeDomain: modelDomainContinuous, SampleTimeMode: string(sampleTimeExplicit),
		A: &a, B: &b, C: &c, D: &d,
		InputNames: &inputs, OutputNames: &outputs, StateNames: &states,
	}

	numerators, _ := NewPolynomialMatrixValue([][][]float64{
		{{1}, {2}},
		{{3}, {4}},
	})
	denominators, _ := NewPolynomialMatrixValue([][][]float64{
		{{1, 1}},
		{{1, 2}},
	})
	delays, _ := NewMatrixValue(2, 2, []float64{0, 0, 0, 0})
	transferParameters := Parameters{
		TimeDomain: modelDomainContinuous, SampleTimeMode: string(sampleTimeExplicit),
		TransferNumerators: &numerators, TransferDenominators: &denominators,
		TransferDelays: &delays, InputNames: &inputs, OutputNames: &outputs,
	}

	zeros, _ := NewComplexRootMatrixValue([][][]complex128{
		{nil, nil},
		{nil, nil},
	})
	poles, _ := NewComplexRootMatrixValue([][][]complex128{
		{{-1}, {-1}},
		{{-2}, {-2}},
	})
	gain, _ := NewMatrixValue(2, 2, []float64{1, 2, 3, 4})
	zpkParameters := Parameters{
		TimeDomain: modelDomainContinuous, SampleTimeMode: string(sampleTimeExplicit),
		Zeros: &zeros, Poles: &poles, D: &gain,
		InputNames: &inputs, OutputNames: &outputs,
	}
	for name, parameters := range map[string]Parameters{
		"state-space": stateParameters,
		"transfer":    transferParameters,
		"zpk":         zpkParameters,
	} {
		var err error
		switch name {
		case "state-space":
			err = validateStateSpaceParameters(parameters)
		case "transfer":
			err = validateMIMOTransferParameters(parameters)
		case "zpk":
			err = validateZPKParameters(parameters)
		}
		if err != nil {
			t.Fatalf("%s parameters: %v", name, err)
		}
	}
	return stateParameters, transferParameters, zpkParameters
}
