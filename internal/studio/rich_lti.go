package studio

import (
	"fmt"
	"math"
	"strings"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

const (
	modelDomainContinuous = "continuous"
	modelDomainDiscrete   = "discrete"

	frequencyUnitRadiansPerSecond = "rad/s"
	responseUnitLinearComplexGain = "linear-complex-gain"
)

func normalizedModelDomain(parameters Parameters) string {
	if parameters.TimeDomain == "" {
		return modelDomainContinuous
	}
	return strings.ToLower(strings.TrimSpace(parameters.TimeDomain))
}

func representationSampleTime(parameters Parameters) float64 {
	if normalizedModelDomain(parameters) == modelDomainDiscrete {
		return parameters.SampleTime
	}
	return 0
}

func representationTimeDomain(parameters Parameters) blockTimeDomain {
	if normalizedModelDomain(parameters) == modelDomainDiscrete {
		return discreteTimeDomain(parameters)
	}
	return continuousTimeDomain()
}

func representationTimeFields() []parameterDefinition {
	return []parameterDefinition{
		{
			Name: "time_domain", Label: "Time domain", Type: "select",
			Options: []parameterOption{
				{Value: modelDomainContinuous, Label: "Continuous"},
				{Value: modelDomainDiscrete, Label: "Discrete"},
			},
			set: func(parameters *Parameters, raw string) error {
				parameters.TimeDomain = strings.ToLower(strings.TrimSpace(raw))
				return nil
			},
			text: func(parameters Parameters) string {
				return normalizedModelDomain(parameters)
			},
		},
		conditionalNumberField(
			"sample_time", "Discrete sample time", "sample time",
			"0.001", MinSimulationSampleTime, "sec",
			func(parameters *Parameters) *float64 { return &parameters.SampleTime },
			func(parameters Parameters) bool {
				return normalizedModelDomain(parameters) == modelDomainDiscrete
			},
		),
	}
}

func validateRepresentationTime(parameters Parameters) error {
	switch normalizedModelDomain(parameters) {
	case modelDomainContinuous:
		return nil
	case modelDomainDiscrete:
		if math.IsNaN(parameters.SampleTime) || math.IsInf(parameters.SampleTime, 0) ||
			parameters.SampleTime <= 0 {
			return invalid("discrete sample time must be a positive finite number")
		}
		return nil
	default:
		return invalid("time domain must be continuous or discrete")
	}
}

func namedLTIPortSchema(parameters Parameters) blockPortSchema {
	if parameters.InputNames == nil || parameters.OutputNames == nil {
		return blockPortSchema{}
	}
	input, _ := newSignalPort(parameters.InputNames.Len(), parameters.InputNames.Names())
	output, _ := newSignalPort(parameters.OutputNames.Len(), parameters.OutputNames.Names())
	return blockPortSchema{inputs: []SignalPort{input}, outputs: []SignalPort{output}}
}

func defaultStateSpaceParameters() Parameters {
	parameters := defaultDiscreteStateSpaceParameters()
	parameters.TimeDomain = modelDomainContinuous
	parameters.SampleTime = 0.1
	return parameters
}

func legacyStateSpaceParameters() Parameters {
	parameters := legacyDiscreteStateSpaceParameters()
	parameters.TimeDomain = modelDomainContinuous
	return parameters
}

func validateStateSpaceParameters(parameters Parameters) error {
	if err := validateRepresentationTime(parameters); err != nil {
		return err
	}
	if parameters.A == nil || parameters.B == nil || parameters.C == nil || parameters.D == nil ||
		parameters.InputNames == nil || parameters.OutputNames == nil || parameters.StateNames == nil {
		return invalid("A, B, C, D and input, output, state names are required")
	}
	states, aColumns := parameters.A.Dims()
	if states != aColumns {
		return invalid("A matrix must be square")
	}
	bRows, inputs := parameters.B.Dims()
	outputs, cColumns := parameters.C.Dims()
	dRows, dColumns := parameters.D.Dims()
	if bRows != states || cColumns != states || dRows != outputs || dColumns != inputs {
		return invalid("state-space dimensions must satisfy A n×n, B n×m, C p×n, D p×m")
	}
	if parameters.InputNames.Len() != inputs ||
		parameters.OutputNames.Len() != outputs ||
		parameters.StateNames.Len() != states {
		return invalid("state-space channel-name counts must match input, output, and state dimensions")
	}
	if err := validateStateSpaceInitialState(parameters, states); err != nil {
		return err
	}
	_, err := stateSpaceFromParameters(parameters)
	if err != nil {
		return fmt.Errorf("controlsys state-space construction: %w", err)
	}
	return nil
}

func stateSpaceFromParameters(parameters Parameters) (*controlsys.System, error) {
	n, _ := parameters.A.Dims()
	_, m := parameters.B.Dims()
	p, _ := parameters.C.Dims()
	system, err := controlsys.New(
		mat.NewDense(n, n, parameters.A.Values()),
		mat.NewDense(n, m, parameters.B.Values()),
		mat.NewDense(p, n, parameters.C.Values()),
		mat.NewDense(p, m, parameters.D.Values()),
		representationSampleTime(parameters),
	)
	if err != nil {
		return nil, err
	}
	system.InputName = parameters.InputNames.Names()
	system.OutputName = parameters.OutputNames.Names()
	system.StateName = parameters.StateNames.Names()
	return system, nil
}

func defaultMIMOTransferParameters() Parameters {
	numerators, _ := NewPolynomialMatrixValue([][][]float64{
		{{1}, {0}},
		{{0}, {1}},
	})
	denominators, _ := NewPolynomialMatrixValue([][][]float64{
		{{1, 1}},
		{{1, 2}},
	})
	delays, _ := NewMatrixValue(2, 2, []float64{0, 0, 0, 0})
	inputs, _ := NewChannelNames([]string{"u1", "u2"})
	outputs, _ := NewChannelNames([]string{"y1", "y2"})
	return Parameters{
		TimeDomain: modelDomainContinuous, SampleTimeMode: string(sampleTimeExplicit),
		SampleTime:         0.1,
		TransferNumerators: &numerators, TransferDenominators: &denominators,
		TransferDelays: &delays, InputNames: &inputs, OutputNames: &outputs,
	}
}

func validateMIMOTransferParameters(parameters Parameters) error {
	if err := validateRepresentationTime(parameters); err != nil {
		return err
	}
	if parameters.TransferNumerators == nil || parameters.TransferDenominators == nil ||
		parameters.TransferDelays == nil ||
		parameters.InputNames == nil || parameters.OutputNames == nil {
		return invalid("numerators, denominators, delays, and input and output names are required")
	}
	outputs, inputs := parameters.TransferNumerators.Dims()
	denominatorRows, denominatorColumns := parameters.TransferDenominators.Dims()
	delayRows, delayColumns := parameters.TransferDelays.Dims()
	if denominatorRows != outputs || denominatorColumns != 1 {
		return invalid("transfer denominator matrix must have one polynomial per output row")
	}
	if delayRows != outputs || delayColumns != inputs {
		return invalid("transfer delay matrix must match the numerator output-by-input shape")
	}
	if parameters.InputNames.Len() != inputs || parameters.OutputNames.Len() != outputs {
		return invalid("transfer channel-name counts must match numerator dimensions")
	}
	numerators := parameters.TransferNumerators.Values()
	denominatorsMatrix := parameters.TransferDenominators.Values()
	for output := range outputs {
		denominator := denominatorsMatrix[output][0]
		if denominator[0] == 0 {
			return invalid("transfer denominator row %d has a zero leading coefficient", output+1)
		}
		for input := range inputs {
			if len(numerators[output][input]) > len(denominator) {
				return invalid(
					"transfer channel %s to %s is improper",
					parameters.InputNames.names[input], parameters.OutputNames.names[output],
				)
			}
			delay := parameters.TransferDelays.At(output, input)
			if delay < 0 {
				return invalid("transfer delays must be nonnegative")
			}
			if normalizedModelDomain(parameters) == modelDomainDiscrete &&
				math.Abs(delay-math.Round(delay)) > 1e-9 {
				return invalid("discrete transfer delays must be integer sample counts")
			}
		}
	}
	_, err := transferSystemFromParameters(parameters)
	if err != nil {
		return fmt.Errorf("controlsys transfer conversion: %w", err)
	}
	return nil
}

func transferFunctionFromParameters(parameters Parameters) *controlsys.TransferFunc {
	denominatorMatrix := parameters.TransferDenominators.Values()
	denominators := make([][]float64, len(denominatorMatrix))
	for output := range denominatorMatrix {
		denominators[output] = append([]float64(nil), denominatorMatrix[output][0]...)
	}
	outputs, inputs := parameters.TransferDelays.Dims()
	delays := make([][]float64, outputs)
	for output := range outputs {
		delays[output] = make([]float64, inputs)
		for input := range inputs {
			delays[output][input] = parameters.TransferDelays.At(output, input)
		}
	}
	return &controlsys.TransferFunc{
		Num: parameters.TransferNumerators.Values(), Den: denominators, Delay: delays,
		Dt:        representationSampleTime(parameters),
		InputName: parameters.InputNames.Names(), OutputName: parameters.OutputNames.Names(),
	}
}

func transferSystemFromParameters(parameters Parameters) (*controlsys.System, error) {
	transfer := transferFunctionFromParameters(parameters)
	if transfer.HasDelay() {
		return delayedTransferSystem(transfer)
	}
	result, err := transfer.StateSpace(nil)
	if err != nil {
		return nil, err
	}
	return result.Sys, nil
}

func delayedTransferSystem(transfer *controlsys.TransferFunc) (*controlsys.System, error) {
	outputs, inputs := transfer.Dims()
	paths := make([]*controlsys.System, 0, outputs*inputs)
	for output := range outputs {
		for input := range inputs {
			pathResult, err := (&controlsys.TransferFunc{
				Num: [][][]float64{{append([]float64(nil), transfer.Num[output][input]...)}},
				Den: [][]float64{append([]float64(nil), transfer.Den[output]...)},
				Dt:  transfer.Dt,
			}).StateSpace(nil)
			if err != nil {
				return nil, fmt.Errorf(
					"realize delayed transfer path %d,%d: %w",
					output+1, input+1, err,
				)
			}
			path := pathResult.Sys
			delay := transfer.Delay[output][input]
			if delay > 0 {
				if err := path.SetInputDelay([]float64{delay}); err != nil {
					return nil, fmt.Errorf(
						"set delayed transfer path %d,%d: %w",
						output+1, input+1, err,
					)
				}
				path, err = path.PullDelaysToLFT()
				if err != nil {
					return nil, fmt.Errorf(
						"prepare delayed transfer path %d,%d: %w",
						output+1, input+1, err,
					)
				}
			}
			paths = append(paths, path)
		}
	}

	augmented, err := controlsys.BlkDiag(paths...)
	if err != nil {
		return nil, fmt.Errorf("assemble delayed transfer paths: %w", err)
	}
	pathCount := outputs * inputs
	inputMap := mat.NewDense(pathCount, inputs, nil)
	outputMap := mat.NewDense(outputs, pathCount, nil)
	for output := range outputs {
		for input := range inputs {
			path := output*inputs + input
			inputMap.Set(path, input, 1)
			outputMap.Set(output, path, 1)
		}
	}

	states, _, _ := augmented.Dims()
	dIntermediate := mat.NewDense(pathCount, inputs, nil)
	dIntermediate.Mul(augmented.D, inputMap)
	d := mat.NewDense(outputs, inputs, nil)
	d.Mul(outputMap, dIntermediate)

	var system *controlsys.System
	if states == 0 {
		system, err = controlsys.NewGain(d, transfer.Dt)
	} else {
		b := mat.NewDense(states, inputs, nil)
		b.Mul(augmented.B, inputMap)
		c := mat.NewDense(outputs, states, nil)
		c.Mul(outputMap, augmented.C)
		system, err = controlsys.New(
			mat.DenseCopyOf(augmented.A), b, c, d, transfer.Dt,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("assemble delayed transfer realization: %w", err)
	}
	if states == 0 {
		delayValues := make([]float64, 0, outputs*inputs)
		for output := range outputs {
			delayValues = append(delayValues, transfer.Delay[output]...)
		}
		if err := system.SetDelay(mat.NewDense(outputs, inputs, delayValues)); err != nil {
			return nil, fmt.Errorf("attach delayed static transfer realization: %w", err)
		}
		system.InputName = append([]string(nil), transfer.InputName...)
		system.OutputName = append([]string(nil), transfer.OutputName...)
		return system, nil
	}

	if augmented.LFT != nil {
		d12 := mat.NewDense(outputs, len(augmented.LFT.Tau), nil)
		d12.Mul(outputMap, augmented.LFT.D12)
		d21 := mat.NewDense(len(augmented.LFT.Tau), inputs, nil)
		d21.Mul(augmented.LFT.D21, inputMap)
		if err := system.SetInternalDelay(
			augmented.LFT.Tau,
			augmented.LFT.B2,
			augmented.LFT.C2,
			d12,
			d21,
			augmented.LFT.D22,
		); err != nil {
			return nil, fmt.Errorf("attach delayed transfer realization: %w", err)
		}
	}
	system.InputName = append([]string(nil), transfer.InputName...)
	system.OutputName = append([]string(nil), transfer.OutputName...)
	return system, nil
}

func defaultZPKParameters() Parameters {
	zeros, _ := NewComplexRootMatrixValue([][][]complex128{
		{nil, nil},
		{nil, nil},
	})
	poles, _ := NewComplexRootMatrixValue([][][]complex128{
		{{-1}, {-1}},
		{{-2}, {-2}},
	})
	gain, _ := NewMatrixValue(2, 2, []float64{1, 0, 0, 1})
	inputs, _ := NewChannelNames([]string{"u1", "u2"})
	outputs, _ := NewChannelNames([]string{"y1", "y2"})
	return Parameters{
		TimeDomain: modelDomainContinuous, SampleTimeMode: string(sampleTimeExplicit),
		SampleTime: 0.1,
		Zeros:      &zeros, Poles: &poles, D: &gain,
		InputNames: &inputs, OutputNames: &outputs,
	}
}

func validateZPKParameters(parameters Parameters) error {
	if err := validateRepresentationTime(parameters); err != nil {
		return err
	}
	if parameters.Zeros == nil || parameters.Poles == nil || parameters.D == nil ||
		parameters.InputNames == nil || parameters.OutputNames == nil {
		return invalid("zeros, poles, gain matrix, and input and output names are required")
	}
	outputs, inputs := parameters.Zeros.Dims()
	poleOutputs, poleInputs := parameters.Poles.Dims()
	gainOutputs, gainInputs := parameters.D.Dims()
	if poleOutputs != outputs || poleInputs != inputs ||
		gainOutputs != outputs || gainInputs != inputs {
		return invalid("ZPK zeros, poles, and gain must have the same output-by-input shape")
	}
	if parameters.InputNames.Len() != inputs || parameters.OutputNames.Len() != outputs {
		return invalid("ZPK channel-name counts must match the gain dimensions")
	}
	_, err := zpkSystemFromParameters(parameters)
	if err != nil {
		return fmt.Errorf("controlsys ZPK conversion: %w", err)
	}
	return nil
}

func zpkFromParameters(parameters Parameters) (*controlsys.ZPK, error) {
	outputs, inputs := parameters.D.Dims()
	gain := make([][]float64, outputs)
	for output := range outputs {
		gain[output] = make([]float64, inputs)
		for input := range inputs {
			gain[output][input] = parameters.D.At(output, input)
		}
	}
	zpk, err := controlsys.NewZPKMIMO(
		parameters.Zeros.Values(), parameters.Poles.Values(), gain,
		representationSampleTime(parameters),
	)
	if err != nil {
		return nil, err
	}
	zpk.InputName = parameters.InputNames.Names()
	zpk.OutputName = parameters.OutputNames.Names()
	return zpk, nil
}

func zpkSystemFromParameters(parameters Parameters) (*controlsys.System, error) {
	zpk, err := zpkFromParameters(parameters)
	if err != nil {
		return nil, err
	}
	result, err := zpk.StateSpace(nil)
	if err != nil {
		return nil, err
	}
	return result.Sys, nil
}

func defaultFRDParameters() Parameters {
	frequencies, _ := NewVectorValue([]float64{0.1, 1, 10})
	response, _ := NewComplexResponseValue(3, 2, 2, []complex128{
		1, 0, 0, 1,
		1, 0, 0, 1,
		1, 0, 0, 1,
	})
	inputs, _ := NewChannelNames([]string{"u1", "u2"})
	outputs, _ := NewChannelNames([]string{"y1", "y2"})
	return Parameters{
		TimeDomain: modelDomainContinuous, SampleTimeMode: string(sampleTimeExplicit),
		SampleTime:    0.1,
		FrequencyUnit: frequencyUnitRadiansPerSecond,
		ResponseUnit:  responseUnitLinearComplexGain,
		Frequencies:   &frequencies, FrequencyResponse: &response,
		InputNames: &inputs, OutputNames: &outputs,
	}
}

func validateFRDParameters(parameters Parameters) error {
	if err := validateRepresentationTime(parameters); err != nil {
		return err
	}
	if parameters.FrequencyUnit != frequencyUnitRadiansPerSecond {
		return invalid("FRD frequency unit must be rad/s")
	}
	if parameters.ResponseUnit != responseUnitLinearComplexGain {
		return invalid("FRD response unit must be linear complex gain")
	}
	if parameters.Frequencies == nil || parameters.FrequencyResponse == nil ||
		parameters.InputNames == nil || parameters.OutputNames == nil {
		return invalid("FRD frequencies, responses, and input and output names are required")
	}
	samples, outputs, inputs := parameters.FrequencyResponse.Dims()
	if samples != parameters.Frequencies.Len() {
		return invalid("FRD frequency count must match the number of response samples")
	}
	if outputs != parameters.OutputNames.Len() || inputs != parameters.InputNames.Len() {
		return invalid("FRD response dimensions must match the input and output names")
	}
	omega := parameters.Frequencies.Values()
	for index := 1; index < len(omega); index++ {
		if omega[index] <= omega[index-1] {
			return invalid("FRD frequencies must be strictly increasing")
		}
	}
	_, err := frdFromParameters(parameters)
	if err != nil {
		return fmt.Errorf("controlsys FRD construction: %w", err)
	}
	return nil
}

func frdFromParameters(parameters Parameters) (*controlsys.FRD, error) {
	frd, err := controlsys.NewFRD(
		parameters.FrequencyResponse.Tensor(),
		parameters.Frequencies.Values(),
		representationSampleTime(parameters),
	)
	if err != nil {
		return nil, err
	}
	frd.InputName = parameters.InputNames.Names()
	frd.OutputName = parameters.OutputNames.Names()
	return frd, nil
}

func realizeFRDBlock(block Block, _ []int) (*controlsys.System, error) {
	if _, err := frdFromParameters(block.Parameters); err != nil {
		return nil, fmt.Errorf("controlsys FRD construction: %w", err)
	}
	return nil, invalid(
		"controlsys FRD is frequency-domain data only; fit or identify a state-space model before time simulation",
	)
}
