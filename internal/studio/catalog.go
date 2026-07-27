package studio

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type BlockDefinition struct {
	Kind        BlockKind
	Label       string
	Category    string
	Description string
	Glyph       string
	Tag         string
	HasInput    bool
	HasOutput   bool
}

type ParameterField struct {
	Name        string
	Label       string
	Type        string
	Value       string
	Step        string
	Min         string
	Max         string
	Unit        string
	Placeholder string
	Help        string
}

type parameterDefinition struct {
	Name        string
	Label       string
	Type        string
	Step        string
	Min         string
	Max         string
	Unit        string
	Placeholder string
	Help        string
}

type blockDefinition struct {
	BlockDefinition
	Defaults   Parameters
	Parameters []parameterDefinition
}

var blockOrder = []BlockKind{
	BlockSource,
	BlockConstant,
	BlockSine,
	BlockGain,
	BlockSum,
	BlockLag,
	BlockIntegrator,
	BlockTransfer,
	BlockPID,
	BlockDelay,
	BlockScope,
	BlockSpectrum,
}

var blockDefinitions = map[BlockKind]blockDefinition{
	BlockSource: {
		BlockDefinition: BlockDefinition{
			Kind: BlockSource, Label: "Step", Category: "Sources",
			Description: "Initial-to-final step", Glyph: "↗", Tag: "SOURCE",
			HasOutput: true,
		},
		Defaults: Parameters{Amplitude: 1},
		Parameters: []parameterDefinition{
			numberField("amplitude", "Final value", "0.05", "-10000", "10000", "scalar"),
			numberField("initial_value", "Initial value", "0.05", "-10000", "10000", "scalar"),
			numberField("step_time", "Step time", "0.05", "0", "120", "sec"),
		},
	},
	BlockConstant: {
		BlockDefinition: BlockDefinition{
			Kind: BlockConstant, Label: "Constant", Category: "Sources",
			Description: "Constant signal", Glyph: "C", Tag: "SOURCE",
			HasOutput: true,
		},
		Defaults: Parameters{Value: 1},
		Parameters: []parameterDefinition{
			numberField("value", "Value", "0.05", "-10000", "10000", "scalar"),
		},
	},
	BlockSine: {
		BlockDefinition: BlockDefinition{
			Kind: BlockSine, Label: "Sine Wave", Category: "Sources",
			Description: "Biased sinusoid", Glyph: "∿", Tag: "SOURCE",
			HasOutput: true,
		},
		Defaults: Parameters{Amplitude: 1, Frequency: 1},
		Parameters: []parameterDefinition{
			numberField("amplitude", "Amplitude", "0.05", "-10000", "10000", "scalar"),
			numberField("bias", "Bias", "0.05", "-10000", "10000", "scalar"),
			numberField("frequency", "Frequency", "0.05", "0", "1000", "rad/s"),
			numberField("phase", "Phase", "0.05", "-1000", "1000", "rad"),
		},
	},
	BlockGain: {
		BlockDefinition: BlockDefinition{
			Kind: BlockGain, Label: "Gain", Category: "Math",
			Description: "Scale a signal", Glyph: "×", Tag: "MATH",
			HasInput: true, HasOutput: true,
		},
		Defaults: Parameters{Gain: 1},
		Parameters: []parameterDefinition{
			numberField("gain", "Gain", "0.05", "-10000", "10000", "scalar"),
		},
	},
	BlockSum: {
		BlockDefinition: BlockDefinition{
			Kind: BlockSum, Label: "Sum", Category: "Math",
			Description: "Signed signal sum", Glyph: "Σ", Tag: "MATH",
			HasInput: true, HasOutput: true,
		},
		Defaults: Parameters{Signs: "+"},
		Parameters: []parameterDefinition{{
			Name: "signs", Label: "Input signs", Type: "text",
			Placeholder: "+-", Help: "Connection order; one sign broadcasts",
		}},
	},
	BlockLag: {
		BlockDefinition: BlockDefinition{
			Kind: BlockLag, Label: "First-order Lag", Category: "Continuous",
			Description: "1 / (τs + 1)", Glyph: "τ", Tag: "CONTINUOUS",
			HasInput: true, HasOutput: true,
		},
		Defaults: Parameters{TimeConstant: 1},
		Parameters: []parameterDefinition{
			numberField("time_constant", "Time constant", "0.05", "0.001", "1000", "sec"),
		},
	},
	BlockIntegrator: {
		BlockDefinition: BlockDefinition{
			Kind: BlockIntegrator, Label: "Integrator", Category: "Continuous",
			Description: "Continuous 1 / s", Glyph: "∫", Tag: "CONTINUOUS",
			HasInput: true, HasOutput: true,
		},
	},
	BlockTransfer: {
		BlockDefinition: BlockDefinition{
			Kind: BlockTransfer, Label: "Transfer Function", Category: "Continuous",
			Description: "Proper SISO model", Glyph: "G", Tag: "CONTINUOUS",
			HasInput: true, HasOutput: true,
		},
		Defaults: Parameters{Numerator: []float64{1}, Denominator: []float64{1, 1}},
		Parameters: []parameterDefinition{
			{
				Name: "numerator", Label: "Numerator coefficients", Type: "text",
				Placeholder: "1, 3", Help: "Descending powers of s",
			},
			{
				Name: "denominator", Label: "Denominator coefficients", Type: "text",
				Placeholder: "1, 2, 1", Help: "Descending powers of s",
			},
		},
	},
	BlockPID: {
		BlockDefinition: BlockDefinition{
			Kind: BlockPID, Label: "PID Controller", Category: "Continuous",
			Description: "Filtered parallel PID", Glyph: "PID", Tag: "CONTROL",
			HasInput: true, HasOutput: true,
		},
		Defaults: Parameters{Proportional: 1, Integral: 0.5, FilterTime: 0.1},
		Parameters: []parameterDefinition{
			numberField("proportional", "Proportional Kp", "0.05", "-10000", "10000", "scalar"),
			numberField("integral", "Integral Ki", "0.05", "-10000", "10000", "1/sec"),
			numberField("derivative", "Derivative Kd", "0.05", "-10000", "10000", "sec"),
			numberField("filter_time", "Derivative filter Tf", "0.01", "0.001", "1000", "sec"),
		},
	},
	BlockDelay: {
		BlockDefinition: BlockDefinition{
			Kind: BlockDelay, Label: "Transport Delay", Category: "Continuous",
			Description: "Padé delay approximation", Glyph: "e⁻ˢ", Tag: "CONTINUOUS",
			HasInput: true, HasOutput: true,
		},
		Defaults: Parameters{Delay: 1, Approximation: 3},
		Parameters: []parameterDefinition{
			numberField("delay", "Delay", "0.05", "0", "120", "sec"),
			{
				Name: "approximation", Label: "Padé order", Type: "number",
				Step: "1", Min: "1", Max: "10", Unit: "order",
			},
		},
	},
	BlockScope: {
		BlockDefinition: BlockDefinition{
			Kind: BlockScope, Label: "Scope", Category: "Sinks",
			Description: "Plot a signal", Glyph: "⌁", Tag: "OUTPUT",
			HasInput: true,
		},
	},
	BlockSpectrum: {
		BlockDefinition: BlockDefinition{
			Kind: BlockSpectrum, Label: "Spectrum Analyzer", Category: "Sinks",
			Description: "Hann-windowed FFT", Glyph: "FFT", Tag: "DSP SINK",
			HasInput: true,
		},
	},
}

func numberField(name, label, step, min, max, unit string) parameterDefinition {
	return parameterDefinition{
		Name: name, Label: label, Type: "number",
		Step: step, Min: min, Max: max, Unit: unit,
	}
}

func BlockLibrary() []BlockDefinition {
	library := make([]BlockDefinition, 0, len(blockOrder))
	for _, kind := range blockOrder {
		library = append(library, blockDefinitions[kind].BlockDefinition)
	}
	return library
}

func (k BlockKind) Definition() BlockDefinition {
	if definition, ok := blockDefinitions[k]; ok {
		return definition.BlockDefinition
	}
	return BlockDefinition{Kind: k, Label: "Unknown", Tag: "UNKNOWN"}
}

func defaultParameters(kind BlockKind) Parameters {
	return cloneParameters(blockDefinitions[kind].Defaults)
}

func cloneParameters(parameters Parameters) Parameters {
	parameters.Numerator = append([]float64(nil), parameters.Numerator...)
	parameters.Denominator = append([]float64(nil), parameters.Denominator...)
	return parameters
}

func (b Block) EditorFields() []ParameterField {
	definition := blockDefinitions[b.Kind]
	fields := make([]ParameterField, 0, len(definition.Parameters))
	for _, field := range definition.Parameters {
		fields = append(fields, ParameterField{
			Name: field.Name, Label: field.Label, Type: field.Type,
			Value: parameterText(b.Parameters, field.Name),
			Step:  field.Step, Min: field.Min, Max: field.Max, Unit: field.Unit,
			Placeholder: field.Placeholder, Help: field.Help,
		})
	}
	return fields
}

func (b Block) Summary() string {
	switch b.Kind {
	case BlockSource:
		if b.Parameters.StepTime == 0 {
			return fmt.Sprintf("%.3g step", b.Parameters.Amplitude)
		}
		return fmt.Sprintf("%.3g at %.3g s", b.Parameters.Amplitude, b.Parameters.StepTime)
	case BlockConstant:
		return fmt.Sprintf("%.3g constant", b.Parameters.Value)
	case BlockSine:
		return fmt.Sprintf("%.3g sin(%.3gt)", b.Parameters.Amplitude, b.Parameters.Frequency)
	case BlockGain:
		return fmt.Sprintf("K = %.3g", b.Parameters.Gain)
	case BlockSum:
		return "signs " + b.Parameters.Signs
	case BlockLag:
		return fmt.Sprintf("τ = %.3g s", b.Parameters.TimeConstant)
	case BlockIntegrator:
		return "1 / s"
	case BlockTransfer:
		return polynomialText(b.Parameters.Numerator) + " / " + polynomialText(b.Parameters.Denominator)
	case BlockPID:
		return fmt.Sprintf("P %.3g · I %.3g · D %.3g",
			b.Parameters.Proportional, b.Parameters.Integral, b.Parameters.Derivative)
	case BlockDelay:
		return fmt.Sprintf("%.3g s · Padé %d", b.Parameters.Delay, b.Parameters.Approximation)
	case BlockScope:
		return "trend output"
	case BlockSpectrum:
		return "frequency output"
	default:
		return ""
	}
}

func validateBlockUpdate(block Block, update BlockUpdate) (Block, error) {
	name := strings.TrimSpace(update.Name)
	if name == "" {
		return Block{}, invalid("block name is required")
	}
	if len(name) > 48 {
		return Block{}, invalid("block name must be 48 characters or fewer")
	}
	definition, ok := blockDefinitions[block.Kind]
	if !ok {
		return Block{}, invalid("unknown block type %q", block.Kind)
	}

	parameters := cloneParameters(block.Parameters)
	for _, field := range definition.Parameters {
		value, exists := update.Parameters[field.Name]
		if !exists {
			return Block{}, invalid("%s is required", strings.ToLower(field.Label))
		}
		if err := setParameter(&parameters, field.Name, value); err != nil {
			return Block{}, err
		}
	}
	if err := validateParameters(block.Kind, parameters); err != nil {
		return Block{}, err
	}
	block.Name = name
	block.Parameters = parameters
	return block, nil
}

func validateParameters(kind BlockKind, parameters Parameters) error {
	switch kind {
	case BlockSource:
		if err := bounded("final value", parameters.Amplitude, -10000, 10000); err != nil {
			return err
		}
		if err := bounded("initial value", parameters.InitialValue, -10000, 10000); err != nil {
			return err
		}
		return bounded("step time", parameters.StepTime, 0, 120)
	case BlockConstant:
		return bounded("value", parameters.Value, -10000, 10000)
	case BlockSine:
		for label, value := range map[string]float64{
			"amplitude": parameters.Amplitude,
			"bias":      parameters.Bias,
			"phase":     parameters.Phase,
		} {
			if err := bounded(label, value, -10000, 10000); err != nil {
				return err
			}
		}
		return bounded("frequency", parameters.Frequency, 0, 1000)
	case BlockGain:
		return bounded("gain", parameters.Gain, -10000, 10000)
	case BlockSum:
		if len(parameters.Signs) == 0 || len(parameters.Signs) > 16 {
			return invalid("input signs must contain 1 to 16 plus or minus signs")
		}
		for _, sign := range parameters.Signs {
			if sign != '+' && sign != '-' {
				return invalid("input signs may contain only + and -")
			}
		}
	case BlockLag:
		return bounded("time constant", parameters.TimeConstant, 0.001, 1000)
	case BlockTransfer:
		if len(parameters.Numerator) == 0 || len(parameters.Denominator) == 0 {
			return invalid("transfer function coefficients are required")
		}
		if len(parameters.Numerator) > 9 || len(parameters.Denominator) > 9 {
			return invalid("transfer functions are limited to eighth order")
		}
		if len(parameters.Numerator) > len(parameters.Denominator) {
			return invalid("transfer function must be proper")
		}
		if parameters.Denominator[0] == 0 {
			return invalid("denominator leading coefficient must be nonzero")
		}
	case BlockPID:
		for label, value := range map[string]float64{
			"proportional gain": parameters.Proportional,
			"integral gain":     parameters.Integral,
			"derivative gain":   parameters.Derivative,
		} {
			if err := bounded(label, value, -10000, 10000); err != nil {
				return err
			}
		}
		if err := bounded("derivative filter", parameters.FilterTime, 0.001, 1000); err != nil {
			return err
		}
	case BlockDelay:
		if err := bounded("delay", parameters.Delay, 0, 120); err != nil {
			return err
		}
		if parameters.Approximation < 1 || parameters.Approximation > 10 {
			return invalid("Padé order must be between 1 and 10")
		}
	}
	return nil
}

func bounded(label string, value, minimum, maximum float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return invalid("%s must be finite", label)
	}
	if value < minimum || value > maximum {
		return invalid("%s must be between %g and %g", label, minimum, maximum)
	}
	return nil
}

func setParameter(parameters *Parameters, name, raw string) error {
	raw = strings.TrimSpace(raw)
	switch name {
	case "signs":
		parameters.Signs = strings.ReplaceAll(raw, " ", "")
		return nil
	case "numerator", "denominator":
		coefficients, err := parseCoefficients(raw)
		if err != nil {
			return invalid("%s coefficients must be comma or space separated numbers", name)
		}
		if name == "numerator" {
			parameters.Numerator = coefficients
		} else {
			parameters.Denominator = coefficients
		}
		return nil
	case "approximation":
		value, err := strconv.Atoi(raw)
		if err != nil {
			return invalid("Padé order must be a whole number")
		}
		parameters.Approximation = value
		return nil
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return invalid("%s must be a number", strings.ReplaceAll(name, "_", " "))
	}
	switch name {
	case "amplitude":
		parameters.Amplitude = value
	case "initial_value":
		parameters.InitialValue = value
	case "step_time":
		parameters.StepTime = value
	case "value":
		parameters.Value = value
	case "bias":
		parameters.Bias = value
	case "frequency":
		parameters.Frequency = value
	case "phase":
		parameters.Phase = value
	case "gain":
		parameters.Gain = value
	case "time_constant":
		parameters.TimeConstant = value
	case "proportional":
		parameters.Proportional = value
	case "integral":
		parameters.Integral = value
	case "derivative":
		parameters.Derivative = value
	case "filter_time":
		parameters.FilterTime = value
	case "delay":
		parameters.Delay = value
	default:
		return invalid("unknown parameter %q", name)
	}
	return nil
}

func parameterText(parameters Parameters, name string) string {
	switch name {
	case "signs":
		return parameters.Signs
	case "numerator":
		return coefficientsText(parameters.Numerator)
	case "denominator":
		return coefficientsText(parameters.Denominator)
	case "approximation":
		return strconv.Itoa(parameters.Approximation)
	}
	var value float64
	switch name {
	case "amplitude":
		value = parameters.Amplitude
	case "initial_value":
		value = parameters.InitialValue
	case "step_time":
		value = parameters.StepTime
	case "value":
		value = parameters.Value
	case "bias":
		value = parameters.Bias
	case "frequency":
		value = parameters.Frequency
	case "phase":
		value = parameters.Phase
	case "gain":
		value = parameters.Gain
	case "time_constant":
		value = parameters.TimeConstant
	case "proportional":
		value = parameters.Proportional
	case "integral":
		value = parameters.Integral
	case "derivative":
		value = parameters.Derivative
	case "filter_time":
		value = parameters.FilterTime
	case "delay":
		value = parameters.Delay
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func parseCoefficients(raw string) ([]float64, error) {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty coefficients")
	}
	coefficients := make([]float64, len(parts))
	for i, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("invalid coefficient")
		}
		coefficients[i] = value
	}
	return coefficients, nil
}

func coefficientsText(coefficients []float64) string {
	parts := make([]string, len(coefficients))
	for i, coefficient := range coefficients {
		parts[i] = strconv.FormatFloat(coefficient, 'g', -1, 64)
	}
	return strings.Join(parts, ", ")
}

func polynomialText(coefficients []float64) string {
	if len(coefficients) == 0 {
		return "?"
	}
	if len(coefficients) > 3 {
		return fmt.Sprintf("order %d", len(coefficients)-1)
	}
	return "[" + coefficientsText(coefficients) + "]"
}
