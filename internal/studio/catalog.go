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

// fieldBound is a numeric parameter's enforced range: the one place that
// states it. numberField derives the editor's Min/Max strings from it and
// parameterDefinition.validateBound enforces it from the same two numbers,
// so an input the editor's attributes accept can never be one the server
// then rejects (or vice versa).
type fieldBound struct {
	// label is the noun bounded()'s error names. It is not always the
	// field's editor Label — e.g. the PID's "proportional" field is
	// captioned "Proportional Kp" in the editor, but its violation reads
	// "proportional gain must be...". Kept as its own value rather than
	// derived from Label, since the two are independently user-visible
	// strings that happen to coincide for most fields but not all.
	label    string
	min, max float64
	value    func(Parameters) float64
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
	// set and text are the field's own read/write: the one place that knows
	// which Parameters member this name maps to. Nothing outside the
	// definition switches on Name again.
	set  func(*Parameters, string) error
	text func(Parameters) string
	// bound is nil for fields with no simple numeric range: text fields,
	// coefficient lists, and the Padé order, whose integer range is a
	// cross-field rule enforced by the block's own validate hook instead.
	bound *fieldBound
}

// validateBound enforces the field's own numeric range, if it has one.
// Fields without a bound (text, coefficients, Padé order) have nothing to
// check here — their rules live in the block's validate hook.
func (field parameterDefinition) validateBound(parameters Parameters) error {
	if field.bound == nil {
		return nil
	}
	return bounded(field.bound.label, field.bound.value(parameters), field.bound.min, field.bound.max)
}

type blockDefinition struct {
	BlockDefinition
	Defaults   Parameters
	Parameters []parameterDefinition
	// validate carries the rules that are not one field's own bound:
	// transfer-function properness and order limits, the sign alphabet and
	// length, the Padé integer range. nil for kinds with no such rule.
	validate func(Parameters) error
	// summary renders the block's one-line canvas caption. nil is never
	// valid for a registered kind — every entry in blockOrder sets one.
	summary func(Parameters) string
}

// minApproximation and maxApproximation bound the transport delay's Padé
// order: the one place that states the range, read by both the editor's
// Min/Max attributes and the validate hook that enforces it.
const (
	minApproximation = 1
	maxApproximation = 10
)

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
			numberField("amplitude", "Final value", "final value", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Amplitude }),
			numberField("initial_value", "Initial value", "initial value", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.InitialValue }),
			numberField("step_time", "Step time", "step time", "0.05", 0, 120, "sec", func(p *Parameters) *float64 { return &p.StepTime }),
		},
		summary: func(parameters Parameters) string {
			if parameters.StepTime == 0 {
				return fmt.Sprintf("%.3g step", parameters.Amplitude)
			}
			return fmt.Sprintf("%.3g at %.3g s", parameters.Amplitude, parameters.StepTime)
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
			numberField("value", "Value", "value", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Value }),
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("%.3g constant", parameters.Value)
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
			numberField("amplitude", "Amplitude", "amplitude", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Amplitude }),
			numberField("bias", "Bias", "bias", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Bias }),
			numberField("frequency", "Frequency", "frequency", "0.05", 0, 1000, "rad/s", func(p *Parameters) *float64 { return &p.Frequency }),
			numberField("phase", "Phase", "phase", "0.05", -1000, 1000, "rad", func(p *Parameters) *float64 { return &p.Phase }),
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("%.3g sin(%.3gt)", parameters.Amplitude, parameters.Frequency)
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
			numberField("gain", "Gain", "gain", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Gain }),
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("K = %.3g", parameters.Gain)
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
			set: func(parameters *Parameters, raw string) error {
				parameters.Signs = strings.ReplaceAll(strings.TrimSpace(raw), " ", "")
				return nil
			},
			text: func(parameters Parameters) string { return parameters.Signs },
		}},
		validate: func(parameters Parameters) error {
			if len(parameters.Signs) == 0 || len(parameters.Signs) > 16 {
				return invalid("input signs must contain 1 to 16 plus or minus signs")
			}
			for _, sign := range parameters.Signs {
				if sign != '+' && sign != '-' {
					return invalid("input signs may contain only + and -")
				}
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			return "signs " + parameters.Signs
		},
	},
	BlockLag: {
		BlockDefinition: BlockDefinition{
			Kind: BlockLag, Label: "First-order Lag", Category: "Continuous",
			Description: "1 / (τs + 1)", Glyph: "τ", Tag: "CONTINUOUS",
			HasInput: true, HasOutput: true,
		},
		Defaults: Parameters{TimeConstant: 1},
		Parameters: []parameterDefinition{
			numberField("time_constant", "Time constant", "time constant", "0.05", 0.001, 1000, "sec", func(p *Parameters) *float64 { return &p.TimeConstant }),
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("τ = %.3g s", parameters.TimeConstant)
		},
	},
	BlockIntegrator: {
		BlockDefinition: BlockDefinition{
			Kind: BlockIntegrator, Label: "Integrator", Category: "Continuous",
			Description: "Continuous 1 / s", Glyph: "∫", Tag: "CONTINUOUS",
			HasInput: true, HasOutput: true,
		},
		summary: func(Parameters) string { return "1 / s" },
	},
	BlockTransfer: {
		BlockDefinition: BlockDefinition{
			Kind: BlockTransfer, Label: "Transfer Function", Category: "Continuous",
			Description: "Proper SISO model", Glyph: "G", Tag: "CONTINUOUS",
			HasInput: true, HasOutput: true,
		},
		Defaults: Parameters{Numerator: []float64{1}, Denominator: []float64{1, 1}},
		Parameters: []parameterDefinition{
			coefficientField("numerator", "Numerator coefficients", "1, 3", func(p *Parameters) *[]float64 { return &p.Numerator }),
			coefficientField("denominator", "Denominator coefficients", "1, 2, 1", func(p *Parameters) *[]float64 { return &p.Denominator }),
		},
		validate: func(parameters Parameters) error {
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
			return nil
		},
		summary: func(parameters Parameters) string {
			return polynomialText(parameters.Numerator) + " / " + polynomialText(parameters.Denominator)
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
			numberField("proportional", "Proportional Kp", "proportional gain", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Proportional }),
			numberField("integral", "Integral Ki", "integral gain", "0.05", -10000, 10000, "1/sec", func(p *Parameters) *float64 { return &p.Integral }),
			numberField("derivative", "Derivative Kd", "derivative gain", "0.05", -10000, 10000, "sec", func(p *Parameters) *float64 { return &p.Derivative }),
			numberField("filter_time", "Derivative filter Tf", "derivative filter", "0.01", 0.001, 1000, "sec", func(p *Parameters) *float64 { return &p.FilterTime }),
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("P %.3g · I %.3g · D %.3g",
				parameters.Proportional, parameters.Integral, parameters.Derivative)
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
			numberField("delay", "Delay", "delay", "0.05", 0, 120, "sec", func(p *Parameters) *float64 { return &p.Delay }),
			{
				Name: "approximation", Label: "Padé order", Type: "number",
				Step: "1", Min: strconv.Itoa(minApproximation), Max: strconv.Itoa(maxApproximation), Unit: "order",
				set: func(parameters *Parameters, raw string) error {
					value, err := strconv.Atoi(strings.TrimSpace(raw))
					if err != nil {
						return invalid("Padé order must be a whole number")
					}
					parameters.Approximation = value
					return nil
				},
				text: func(parameters Parameters) string { return strconv.Itoa(parameters.Approximation) },
			},
		},
		validate: func(parameters Parameters) error {
			if parameters.Approximation < minApproximation || parameters.Approximation > maxApproximation {
				return invalid("Padé order must be between %d and %d", minApproximation, maxApproximation)
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("%.3g s · Padé %d", parameters.Delay, parameters.Approximation)
		},
	},
	BlockScope: {
		BlockDefinition: BlockDefinition{
			Kind: BlockScope, Label: "Scope", Category: "Sinks",
			Description: "Plot a signal", Glyph: "⌁", Tag: "OUTPUT",
			HasInput: true,
		},
		summary: func(Parameters) string { return "trend output" },
	},
	BlockSpectrum: {
		BlockDefinition: BlockDefinition{
			Kind: BlockSpectrum, Label: "Spectrum Analyzer", Category: "Sinks",
			Description: "Hann-windowed FFT", Glyph: "FFT", Tag: "DSP SINK",
			HasInput: true,
		},
		summary: func(Parameters) string { return "frequency output" },
	},
}

// numberField builds a scalar float field from a selector picking its home
// in Parameters, so the block definition stays the only place that names it.
// min and max are the field's one range authority: the editor's Min/Max
// attributes and validateBound's enforcement both derive from these two
// numbers, so the range cannot state itself two different ways. boundsLabel
// is kept distinct from label because the two are independently user-visible
// strings — see fieldBound's comment.
func numberField(name, label, boundsLabel, step string, min, max float64, unit string, field func(*Parameters) *float64) parameterDefinition {
	return parameterDefinition{
		Name: name, Label: label, Type: "number",
		Step: step, Min: formatFloat(min), Max: formatFloat(max), Unit: unit,
		set: func(parameters *Parameters, raw string) error {
			value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil {
				return invalid("%s must be a number", strings.ReplaceAll(name, "_", " "))
			}
			*field(parameters) = value
			return nil
		},
		text: func(parameters Parameters) string {
			return formatFloat(*field(&parameters))
		},
		bound: &fieldBound{
			label: boundsLabel, min: min, max: max,
			value: func(parameters Parameters) float64 { return *field(&parameters) },
		},
	}
}

// formatFloat renders a float64 the same way whether it backs a live
// parameter value or a field's static bound, so an editor's Min/Max
// attribute and its current value always agree on how a number like -10000
// or 0.001 prints.
func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// coefficientField is numberField's counterpart for the polynomial
// parameters: same one-selector shape, but parsed and rendered as a
// comma/space separated coefficient list instead of a single number.
func coefficientField(name, label, placeholder string, field func(*Parameters) *[]float64) parameterDefinition {
	return parameterDefinition{
		Name: name, Label: label, Type: "text",
		Placeholder: placeholder, Help: "Descending powers of s",
		set: func(parameters *Parameters, raw string) error {
			coefficients, err := parseCoefficients(strings.TrimSpace(raw))
			if err != nil {
				return invalid("%s coefficients must be comma or space separated numbers", name)
			}
			*field(parameters) = coefficients
			return nil
		},
		text: func(parameters Parameters) string {
			return coefficientsText(*field(&parameters))
		},
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
			Value: field.text(b.Parameters),
			Step:  field.Step, Min: field.Min, Max: field.Max, Unit: field.Unit,
			Placeholder: field.Placeholder, Help: field.Help,
		})
	}
	return fields
}

func (b Block) Summary() string {
	definition, ok := blockDefinitions[b.Kind]
	if !ok || definition.summary == nil {
		return ""
	}
	return definition.summary(b.Parameters)
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
		if err := field.set(&parameters, value); err != nil {
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

// validateParameters is the one entry point both the editor path
// (validateBlockUpdate) and the compile path (simulate.go's compileFlow) call
// to enforce a block's rules: each field's own bound first, in the order the
// definition lists them, then the block's cross-field validate hook.
func validateParameters(kind BlockKind, parameters Parameters) error {
	definition, ok := blockDefinitions[kind]
	if !ok {
		return nil
	}
	for _, field := range definition.Parameters {
		if err := field.validateBound(parameters); err != nil {
			return err
		}
	}
	if definition.validate == nil {
		return nil
	}
	return definition.validate(parameters)
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
