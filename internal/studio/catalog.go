package studio

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

type BlockDefinition struct {
	Kind        BlockKind
	Label       string
	Category    string
	Description string
	Glyph       string
	Tag         string
}

// HasInput and HasOutput are the workbench template's and palette's window
// into a block's structural role: which port glyphs to draw. Both delegate
// to the same Kind-level derivation Connect and compileModel enforce, so the
// canvas can never draw a port that the wiring rules would then refuse.
func (d BlockDefinition) HasInput() bool  { return d.Kind.HasInput() }
func (d BlockDefinition) HasOutput() bool { return d.Kind.HasOutput() }

type ParameterField struct {
	Name        string
	Label       string
	Type        string
	Value       string
	Options     []ParameterOption
	Rows        int
	Columns     int
	Multiline   bool
	Step        string
	Min         string
	Max         string
	Unit        string
	Placeholder string
	Help        string
}

type ParameterOption struct {
	Value    string
	Label    string
	Selected bool
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
	Options     []parameterOption
	active      func(Parameters) bool
	shape       func(Parameters) (int, int)
	// set and text are the field's own read/write: the one place that knows
	// which Parameters member this name maps to. Nothing outside the
	// definition switches on Name again.
	set  func(*Parameters, string) error
	text func(Parameters) string
	// bound is nil for fields with no simple numeric range: text fields,
	// coefficient lists, and approximation order, whose integer range is a
	// cross-field rule enforced by the block's own validate hook instead.
	bound *fieldBound
}

type parameterOption struct {
	Value string
	Label string
}

// validateBound enforces the field's own numeric range, if it has one.
// Fields without a bound (text, coefficients, Padé order) have nothing to
// check here — their rules live in the block's validate hook.
func (field parameterDefinition) validateBound(parameters Parameters) error {
	if field.bound == nil {
		return nil
	}
	value := field.bound.value(parameters)
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return invalid("%s must be finite", field.bound.label)
	}
	if field.active != nil && !field.active(parameters) {
		return nil
	}
	return bounded(field.bound.label, field.bound.value(parameters), field.bound.min, field.bound.max)
}

type blockDefinition struct {
	BlockDefinition
	Defaults   Parameters
	Parameters []parameterDefinition
	// role is the block's part in compileModel's structural rules: at least
	// one roleSource and one roleSink block must be present before
	// simulating, and a roleSource block may not accept a connection. The
	// zero value, roleDynamic, covers every block that is neither — Gain,
	// Sum, Lag, Integrator, Transfer, PID, and Delay all take one input and
	// produce one output like any other interior block.
	role blockRole
	// variadic is true for the one kind whose input count is not fixed —
	// Sum today, and any future block like Product or Mux that combines an
	// arbitrary number of connected inputs. Every other non-source kind
	// accepts exactly one; see arity, which folds this together with role
	// into the none/one/variadic answer Connect and compileModel both
	// consult instead of separately special-casing Sum by name.
	variadic bool
	// inputPorts answers how many input terminals a variadic kind exposes
	// for a given set of parameters — Sum, one per sign character. Setting
	// variadic without setting this is a programming error, guarded by
	// TestEveryVariadicKindDerivesItsInputPortsFromParameters. Fixed-arity
	// kinds leave it nil: arity already states their count, so only a kind
	// whose count a user can change needs to say how it is derived.
	inputPorts func(Parameters) int
	// declareWiredPorts widens a variadic kind's parameters so its port list
	// covers wires already sitting on ports the parameters never named — the
	// state a database written before connections carried ports can be in.
	// It reports false when the widening cannot be done without changing what
	// the block computes, which leaves the stored parameters alone. Sum is
	// the only kind that can widen: a lone sign is shorthand for that same
	// sign on every wire, so repeating it names each port and sums exactly
	// the same signals.
	declareWiredPorts func(Parameters, int) (Parameters, bool)
	// portSchema derives terminal widths and channel names from validated
	// parameters. nil uses the scalar arity defaults.
	portSchema func(Parameters) blockPortSchema
	// realize builds the block's controlsys realization from its own
	// parameters and the input ports its wires land on: ascending, distinct,
	// and never negative, which compileModel establishes before calling so a
	// hook can index by port without re-checking. The ports are what a
	// variadic kind needs: Sum's signs are its ports, so the sign an input
	// carries has to come from the terminal it arrived on rather than from its
	// place in the list. A fixed-arity kind reads neither. nil means the block
	// has no dynamics of its own: every source and sink realizes as a unit
	// gain, and realizeSystem supplies that default rather than each of the
	// five repeating it.
	realize func(Block, []int) (*controlsys.System, error)
	// timeDomain is the catalog's declaration of a block's domain contract.
	// nil means domain-neutral: the compiler may place the block in a
	// continuous system or retime its static gain to one discrete rate.
	timeDomain func(Parameters) blockTimeDomain
	// step creates a per-sample evaluator for behavior that cannot remain in
	// an LTI controlsys segment. Existing blocks are all LTI and leave it nil.
	step func(Block, float64) (stepEvaluator, error)
	// waveform evaluates a roleSource block's signal at time t. nil for
	// every other role.
	waveform func(Parameters, int, float64) float64
	// spectrum is true for the one sink kind whose output is a frequency
	// spectrum instead of a time series and settling metric. It is a
	// property of this specific kind, not the source/dynamic/sink
	// structural role above, so it is its own field rather than a fourth
	// role value.
	spectrum bool
	// validate carries the rules that are not one field's own bound:
	// transfer-function properness and order limits, the sign alphabet and
	// length, the Padé integer range. nil for kinds with no such rule.
	validate func(Parameters) error
	// checkInputs enforces a kind's own rule tying its parameters to the
	// number of connected inputs, once compileModel's arity walk has already
	// confirmed the count itself satisfies the kind's arity. nil for every
	// kind except Sum, whose signs must be length 1 (broadcasting to every
	// input) or exactly the connected input count — Sum's own concern, not
	// a generic arity rule every block shares.
	checkInputs func(Block, int) error
	// summary renders the block's one-line canvas caption. nil is never
	// valid for a registered kind — every entry in blockOrder sets one.
	summary func(Parameters) string
}

// blockRole is source | dynamic | sink: see the role field's comment on
// blockDefinition for what each value governs. roleDynamic is the zero
// value because most registered kinds are it.
type blockRole int

const (
	roleDynamic blockRole = iota
	roleSource
	roleSink
)

// realizeSystem builds the block's controlsys realization, defaulting to a
// unit gain when the definition sets no realize of its own. Every source and
// every sink shares that pass-through behavior, so it is stated once here
// instead of five block entries repeating the same three lines.
func (d blockDefinition) realizeSystem(block Block, ports []int) (*controlsys.System, error) {
	if d.realize != nil {
		return d.realize(block, ports)
	}
	return controlsys.NewGain(mat.NewDense(1, 1, []float64{1}), 0)
}

func (d blockDefinition) domain(parameters Parameters) blockTimeDomain {
	if d.timeDomain == nil {
		return neutralTimeDomain()
	}
	return d.timeDomain(parameters)
}

// isSource and isSink read a block's structural role, replacing the two
// kind-list functions that used to enumerate source and sink kinds by hand
// in simulate.go. The catalog is now the only place a kind's role is stated.
func (k BlockKind) isSource() bool { return blockDefinitions[k].role == roleSource }
func (k BlockKind) isSink() bool   { return blockDefinitions[k].role == roleSink }

// isSpectrumSink reports whether a sink's output is a frequency spectrum
// rather than a time series and settling metric — see the spectrum field's
// comment on blockDefinition for why this is not folded into role.
func (k BlockKind) isSpectrumSink() bool { return blockDefinitions[k].spectrum }
func (k BlockKind) isStepBlock() bool    { return blockDefinitions[k].step != nil }

// inputArity states how many incoming connections a block accepts. Connect's
// incoming-count check (studio.go) and compileModel's arity walk (simulate.go)
// both consult this one derivation instead of separately re-deriving "every
// non-Sum block takes one input."
type inputArity int

const (
	// arityOne is the zero value: most registered kinds — Gain, Lag,
	// Integrator, Transfer, PID, Delay, Scope, and Spectrum — accept
	// exactly one connected input.
	arityOne      inputArity = iota
	arityNone                // sources: no incoming connection is permitted
	arityVariadic            // Sum, and any future kind like it: any number of inputs
)

// arity folds a block's role and variadic flag into the three-way answer
// Connect and compileModel need: none for a source, variadic for the one
// kind that sets variadic, and exactly one for everything else.
func (d blockDefinition) arity() inputArity {
	switch {
	case d.role == roleSource:
		return arityNone
	case d.variadic:
		return arityVariadic
	default:
		return arityOne
	}
}

func (k BlockKind) arity() inputArity { return blockDefinitions[k].arity() }

// inputPortCount is the one authority for how many input terminals a block
// carries: none for a source, one for a fixed-arity kind, and whatever the
// kind's own inputPorts hook derives from the parameters for a variadic one.
// Connect refuses a wire to any index outside it, UpdateBlock refuses an edit
// that would shrink it past a wired port, and the workbench draws exactly
// this many glyphs — so a port a user can see is always a port the wiring
// rules accept, and one they cannot see can never be wired behind their back.
func (d blockDefinition) ports(parameters Parameters) blockPortSchema {
	if d.portSchema != nil {
		return d.portSchema(parameters)
	}
	inputs := 1
	switch d.arity() {
	case arityNone:
		inputs = 0
	case arityVariadic:
		inputs = d.inputPorts(parameters)
	}
	outputs := 1
	if d.role == roleSink {
		outputs = 0
	}
	return scalarPortSchema(inputs, outputs)
}

// InputPortCount and OutputPortCount are a placed block's own terminals, as
// its parameters currently stand. They are the workbench's and the wiring
// rules' shared window onto the derivation above, so the canvas cannot draw a
// port Connect would refuse.
func (b Block) InputPortCount() int  { return len(b.portSchema().inputs) }
func (b Block) OutputPortCount() int { return len(b.portSchema().outputs) }

func (b Block) hasInputPort(port int) bool  { return port >= 0 && port < b.InputPortCount() }
func (b Block) hasOutputPort(port int) bool { return port >= 0 && port < b.OutputPortCount() }

// minApproximation and maxApproximation bound both finite-order delay
// representations: the one place that states the range, read by both the
// editor and the validation hook.
const (
	minApproximation = 1
	maxApproximation = 10

	delayModeExact  = "exact"
	delayModePade   = "pade"
	delayModeThiran = "thiran"
)

func normalizedDelayMode(parameters Parameters) string {
	if parameters.DelayMode == "" {
		// Existing flows stored only Delay and Approximation, whose historical
		// meaning was Padé. New blocks carry the explicit exact default.
		return delayModePade
	}
	return strings.ToLower(strings.TrimSpace(parameters.DelayMode))
}

// maxInputSigns bounds how many inputs a Sum can name, and so how many input
// ports it can expose. The one place that states it: the sign field's own
// validate hook enforces it and declareWiredPorts refuses to widen past it,
// rather than the two agreeing by coincidence.
const maxInputSigns = 16

func identityDense(width int) *mat.Dense {
	values := make([]float64, width*width)
	for channel := range width {
		values[channel*width+channel] = 1
	}
	return mat.NewDense(width, width, values)
}

func defaultMatrixGainParameters() Parameters {
	matrix, err := NewMatrixValue(2, 2, []float64{1, 0, 0, 1})
	if err != nil {
		panic(err)
	}
	inputs, err := NewChannelNames([]string{"u1", "u2"})
	if err != nil {
		panic(err)
	}
	outputs, err := NewChannelNames([]string{"y1", "y2"})
	if err != nil {
		panic(err)
	}
	return Parameters{D: &matrix, InputNames: &inputs, OutputNames: &outputs}
}

func defaultVectorConstantParameters() Parameters {
	values, err := NewVectorValue([]float64{1, 0})
	if err != nil {
		panic(err)
	}
	outputs, err := NewChannelNames([]string{"u1", "u2"})
	if err != nil {
		panic(err)
	}
	return Parameters{Vector: &values, OutputNames: &outputs}
}

func defaultVectorScopeParameters() Parameters {
	inputs, err := NewChannelNames([]string{"y1", "y2"})
	if err != nil {
		panic(err)
	}
	return Parameters{InputNames: &inputs}
}

func defaultVectorSumParameters() Parameters {
	inputs, err := NewChannelNames([]string{"x1", "x2"})
	if err != nil {
		panic(err)
	}
	outputs, err := NewChannelNames([]string{"y1", "y2"})
	if err != nil {
		panic(err)
	}
	return Parameters{Signs: "+-", InputNames: &inputs, OutputNames: &outputs}
}

func defaultRoutingParameters() Parameters {
	inputs, err := NewChannelNames([]string{"u1", "u2"})
	if err != nil {
		panic(err)
	}
	outputs, err := NewChannelNames([]string{"u2", "u1"})
	if err != nil {
		panic(err)
	}
	return Parameters{InputNames: &inputs, OutputNames: &outputs}
}

func defaultMuxParameters() Parameters {
	outputs, err := NewChannelNames([]string{"u1", "u2"})
	if err != nil {
		panic(err)
	}
	return Parameters{OutputNames: &outputs}
}

func defaultDemuxParameters() Parameters {
	inputs, err := NewChannelNames([]string{"u1", "u2"})
	if err != nil {
		panic(err)
	}
	return Parameters{InputNames: &inputs}
}

func routingGain(inputNames, outputNames []string) (*mat.Dense, error) {
	inputIndex := make(map[string]int, len(inputNames))
	for index, name := range inputNames {
		inputIndex[name] = index
	}
	values := make([]float64, len(outputNames)*len(inputNames))
	for output, name := range outputNames {
		input, ok := inputIndex[name]
		if !ok {
			return nil, invalid("output channel %q is not present in the input channels", name)
		}
		values[output*len(inputNames)+input] = 1
	}
	return mat.NewDense(len(outputNames), len(inputNames), values), nil
}

func routingPortSchema(parameters Parameters) blockPortSchema {
	if parameters.InputNames == nil || parameters.OutputNames == nil {
		return blockPortSchema{}
	}
	input, _ := newSignalPort(
		parameters.InputNames.Len(),
		parameters.InputNames.Names(),
	)
	output, _ := newSignalPort(
		parameters.OutputNames.Len(),
		parameters.OutputNames.Names(),
	)
	return blockPortSchema{inputs: []SignalPort{input}, outputs: []SignalPort{output}}
}

func realizeRoutingBlock(block Block, _ []int) (*controlsys.System, error) {
	gain, err := routingGain(
		block.Parameters.InputNames.Names(),
		block.Parameters.OutputNames.Names(),
	)
	if err != nil {
		return nil, err
	}
	return controlsys.NewGain(gain, 0)
}

func validateSelectorParameters(parameters Parameters) error {
	if parameters.InputNames == nil || parameters.OutputNames == nil {
		return invalid("input and output channel names are required")
	}
	_, err := routingGain(
		parameters.InputNames.Names(),
		parameters.OutputNames.Names(),
	)
	return err
}

func defaultDiscreteStateSpaceParameters() Parameters {
	a, _ := NewMatrixValue(2, 2, []float64{0.8, 0, 0, 0.5})
	b, _ := NewMatrixValue(2, 2, []float64{1, 0, 0, 1})
	c, _ := NewMatrixValue(2, 2, []float64{1, 0, 0, 1})
	d, _ := NewMatrixValue(2, 2, []float64{0, 0, 0, 0})
	inputs, _ := NewChannelNames([]string{"u1", "u2"})
	outputs, _ := NewChannelNames([]string{"y1", "y2"})
	states, _ := NewChannelNames([]string{"x1", "x2"})
	return Parameters{
		A: &a, B: &b, C: &c, D: &d,
		InputNames: &inputs, OutputNames: &outputs, StateNames: &states,
		SampleTime: 0.1, SampleTimeMode: string(sampleTimeExplicit),
	}
}

func validateDiscreteStateSpaceParameters(parameters Parameters) error {
	if err := validateDiscreteSampleTime(parameters); err != nil {
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
	if bRows != states || cColumns != states ||
		dRows != outputs || dColumns != inputs {
		return invalid(
			"state-space dimensions must satisfy A n×n, B n×m, C p×n, D p×m",
		)
	}
	if parameters.InputNames.Len() != inputs ||
		parameters.OutputNames.Len() != outputs ||
		parameters.StateNames.Len() != states {
		return invalid(
			"state-space channel-name counts must match input, output, and state dimensions",
		)
	}
	return nil
}

func validateDiscretizedTransferParameters(parameters Parameters) error {
	if err := validateDiscreteSampleTime(parameters); err != nil {
		return err
	}
	if len(parameters.Numerator) == 0 || len(parameters.Denominator) == 0 {
		return invalid("transfer function coefficients are required")
	}
	if len(parameters.Numerator) > len(parameters.Denominator) {
		return invalid("transfer function must be proper")
	}
	if parameters.Denominator[0] == 0 {
		return invalid("denominator leading coefficient must be nonzero")
	}
	switch controlsys.C2DMethod(parameters.ConversionMethod) {
	case controlsys.C2DMethodZOH,
		controlsys.C2DMethodFOH,
		controlsys.C2DMethodMatched,
		controlsys.C2DMethodImpulse:
		return nil
	default:
		return invalid("conversion method must be zoh, foh, matched, or impulse")
	}
}

func realizeDiscretizedTransfer(block Block, _ []int) (*controlsys.System, error) {
	result, err := (&controlsys.TransferFunc{
		Num: [][][]float64{{append([]float64(nil), block.Parameters.Numerator...)}},
		Den: [][]float64{append([]float64(nil), block.Parameters.Denominator...)},
	}).StateSpace(nil)
	if err != nil {
		return nil, err
	}
	switch controlsys.C2DMethod(block.Parameters.ConversionMethod) {
	case controlsys.C2DMethodZOH:
		return result.Sys.DiscretizeZOH(block.Parameters.SampleTime)
	case controlsys.C2DMethodFOH:
		return result.Sys.DiscretizeFOH(block.Parameters.SampleTime)
	case controlsys.C2DMethodMatched:
		return result.Sys.DiscretizeMatched(block.Parameters.SampleTime)
	case controlsys.C2DMethodImpulse:
		return result.Sys.DiscretizeImpulse(block.Parameters.SampleTime)
	default:
		return nil, invalid("unsupported conversion method %q", block.Parameters.ConversionMethod)
	}
}

var blockOrder = []BlockKind{
	BlockSource,
	BlockConstant,
	BlockVectorConstant,
	BlockSine,
	BlockGain,
	BlockMatrixGain,
	BlockMux,
	BlockDemux,
	BlockSelector,
	BlockPermutation,
	BlockSum,
	BlockVectorSum,
	BlockLag,
	BlockIntegrator,
	BlockTransfer,
	BlockPID,
	BlockPID2,
	BlockDelay,
	BlockStateSpace,
	BlockMIMOTransfer,
	BlockZPK,
	BlockFRD,
	BlockUnitDelay,
	BlockDiscreteTransfer,
	BlockDiscreteStateSpace,
	BlockDiscretizedTransfer,
	BlockScope,
	BlockVectorScope,
	BlockSpectrum,
}

var blockDefinitions = map[BlockKind]blockDefinition{
	BlockSource: {
		BlockDefinition: BlockDefinition{
			Kind: BlockSource, Label: "Step", Category: "Sources",
			Description: "Initial-to-final step", Glyph: "↗", Tag: "SOURCE",
		},
		Defaults: Parameters{Amplitude: 1},
		Parameters: []parameterDefinition{
			numberField("amplitude", "Final value", "final value", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Amplitude }),
			numberField("initial_value", "Initial value", "initial value", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.InitialValue }),
			numberField("step_time", "Step time", "step time", "0.05", 0, MaxSimulationDuration, "sec", func(p *Parameters) *float64 { return &p.StepTime }),
		},
		role: roleSource,
		waveform: func(parameters Parameters, _ int, t float64) float64 {
			if t < parameters.StepTime {
				return parameters.InitialValue
			}
			return parameters.Amplitude
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
		},
		Defaults: Parameters{Value: 1},
		Parameters: []parameterDefinition{
			numberField("value", "Value", "value", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Value }),
		},
		role:     roleSource,
		waveform: func(parameters Parameters, _ int, _ float64) float64 { return parameters.Value },
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("%.3g constant", parameters.Value)
		},
	},
	BlockVectorConstant: {
		BlockDefinition: BlockDefinition{
			Kind: BlockVectorConstant, Label: "Vector Constant", Category: "Sources",
			Description: "Named constant vector", Glyph: "Cv", Tag: "MIMO SOURCE",
		},
		Defaults: defaultVectorConstantParameters(),
		Parameters: []parameterDefinition{
			vectorField("vector", "Values", func(parameters *Parameters) **VectorValue {
				return &parameters.Vector
			}),
			channelNamesField("output_names", "Output channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.OutputNames
			}),
		},
		role: roleSource,
		portSchema: func(parameters Parameters) blockPortSchema {
			if parameters.Vector == nil || parameters.OutputNames == nil {
				return blockPortSchema{}
			}
			output, _ := newSignalPort(parameters.Vector.Len(), parameters.OutputNames.Names())
			return blockPortSchema{outputs: []SignalPort{output}}
		},
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			width := block.Parameters.Vector.Len()
			return controlsys.NewGain(identityDense(width), 0)
		},
		waveform: func(parameters Parameters, channel int, _ float64) float64 {
			return parameters.Vector.values[channel]
		},
		validate: func(parameters Parameters) error {
			if parameters.Vector == nil || parameters.OutputNames == nil {
				return invalid("vector values and output channel names are required")
			}
			if parameters.Vector.Len() != parameters.OutputNames.Len() {
				return invalid(
					"vector has %d values but %d output channel names",
					parameters.Vector.Len(), parameters.OutputNames.Len(),
				)
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("%d-channel constant", parameters.Vector.Len())
		},
	},
	BlockSine: {
		BlockDefinition: BlockDefinition{
			Kind: BlockSine, Label: "Sine Wave", Category: "Sources",
			Description: "Biased sinusoid", Glyph: "∿", Tag: "SOURCE",
		},
		Defaults: Parameters{Amplitude: 1, Frequency: 1},
		Parameters: []parameterDefinition{
			numberField("amplitude", "Amplitude", "amplitude", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Amplitude }),
			numberField("bias", "Bias", "bias", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Bias }),
			numberField("frequency", "Frequency", "frequency", "0.05", 0, 1000, "rad/s", func(p *Parameters) *float64 { return &p.Frequency }),
			numberField("phase", "Phase", "phase", "0.05", -1000, 1000, "rad", func(p *Parameters) *float64 { return &p.Phase }),
		},
		role: roleSource,
		waveform: func(parameters Parameters, _ int, t float64) float64 {
			return parameters.Bias + parameters.Amplitude*math.Sin(parameters.Frequency*t+parameters.Phase)
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("%.3g sin(%.3gt)", parameters.Amplitude, parameters.Frequency)
		},
	},
	BlockGain: {
		BlockDefinition: BlockDefinition{
			Kind: BlockGain, Label: "Gain", Category: "Math",
			Description: "Scale a signal", Glyph: "×", Tag: "MATH",
		},
		Defaults: Parameters{Gain: 1},
		Parameters: []parameterDefinition{
			numberField("gain", "Gain", "gain", "0.05", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Gain }),
		},
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			return controlsys.NewGain(mat.NewDense(1, 1, []float64{block.Parameters.Gain}), 0)
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("K = %.3g", parameters.Gain)
		},
	},
	BlockMatrixGain: {
		BlockDefinition: BlockDefinition{
			Kind: BlockMatrixGain, Label: "Matrix Gain", Category: "Math",
			Description: "Named vector gain y = Du", Glyph: "D", Tag: "MIMO",
		},
		Defaults: defaultMatrixGainParameters(),
		Parameters: []parameterDefinition{
			matrixField("d", "Gain matrix D", func(parameters *Parameters) **MatrixValue {
				return &parameters.D
			}),
			channelNamesField("input_names", "Input channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.InputNames
			}),
			channelNamesField("output_names", "Output channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.OutputNames
			}),
		},
		portSchema: func(parameters Parameters) blockPortSchema {
			if parameters.D == nil || parameters.InputNames == nil || parameters.OutputNames == nil {
				return blockPortSchema{}
			}
			rows, columns := parameters.D.Dims()
			input, _ := newSignalPort(columns, parameters.InputNames.Names())
			output, _ := newSignalPort(rows, parameters.OutputNames.Names())
			return blockPortSchema{
				inputs: []SignalPort{input}, outputs: []SignalPort{output},
			}
		},
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			rows, columns := block.Parameters.D.Dims()
			return controlsys.NewGain(
				mat.NewDense(rows, columns, block.Parameters.D.Values()),
				0,
			)
		},
		validate: func(parameters Parameters) error {
			if parameters.D == nil || parameters.InputNames == nil || parameters.OutputNames == nil {
				return invalid("gain matrix and input/output channel names are required")
			}
			rows, columns := parameters.D.Dims()
			if parameters.InputNames.Len() != columns {
				return invalid(
					"gain matrix has %d columns but %d input channel names",
					columns, parameters.InputNames.Len(),
				)
			}
			if parameters.OutputNames.Len() != rows {
				return invalid(
					"gain matrix has %d rows but %d output channel names",
					rows, parameters.OutputNames.Len(),
				)
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			if parameters.D == nil {
				return "invalid matrix"
			}
			rows, columns := parameters.D.Dims()
			return fmt.Sprintf("%d×%d named gain", rows, columns)
		},
	},
	BlockMux: {
		BlockDefinition: BlockDefinition{
			Kind: BlockMux, Label: "Mux", Category: "Routing",
			Description: "Assemble named scalar channels", Glyph: "M", Tag: "MIMO ROUTING",
		},
		Defaults: defaultMuxParameters(),
		Parameters: []parameterDefinition{
			channelNamesField("output_names", "Output channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.OutputNames
			}),
		},
		variadic: true,
		inputPorts: func(parameters Parameters) int {
			if parameters.OutputNames == nil {
				return 0
			}
			return parameters.OutputNames.Len()
		},
		portSchema: func(parameters Parameters) blockPortSchema {
			if parameters.OutputNames == nil {
				return blockPortSchema{}
			}
			names := parameters.OutputNames.Names()
			inputs := make([]SignalPort, len(names))
			for port, name := range names {
				inputs[port], _ = newSignalPort(1, []string{name})
			}
			output, _ := newSignalPort(len(names), names)
			return blockPortSchema{inputs: inputs, outputs: []SignalPort{output}}
		},
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			return controlsys.NewGain(identityDense(block.Parameters.OutputNames.Len()), 0)
		},
		validate: func(parameters Parameters) error {
			if parameters.OutputNames == nil {
				return invalid("output channel names are required")
			}
			return nil
		},
		checkInputs: func(block Block, inputs int) error {
			want := block.Parameters.OutputNames.Len()
			if inputs != want {
				return invalid("%s needs one scalar input for each of its %d output channels", block.Name, want)
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("assemble %d channels", parameters.OutputNames.Len())
		},
	},
	BlockDemux: {
		BlockDefinition: BlockDefinition{
			Kind: BlockDemux, Label: "Demux", Category: "Routing",
			Description: "Decompose a named vector", Glyph: "D", Tag: "MIMO ROUTING",
		},
		Defaults: defaultDemuxParameters(),
		Parameters: []parameterDefinition{
			channelNamesField("input_names", "Input channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.InputNames
			}),
		},
		portSchema: func(parameters Parameters) blockPortSchema {
			if parameters.InputNames == nil {
				return blockPortSchema{}
			}
			names := parameters.InputNames.Names()
			input, _ := newSignalPort(len(names), names)
			outputs := make([]SignalPort, len(names))
			for port, name := range names {
				outputs[port], _ = newSignalPort(1, []string{name})
			}
			return blockPortSchema{inputs: []SignalPort{input}, outputs: outputs}
		},
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			return controlsys.NewGain(identityDense(block.Parameters.InputNames.Len()), 0)
		},
		validate: func(parameters Parameters) error {
			if parameters.InputNames == nil {
				return invalid("input channel names are required")
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("decompose %d channels", parameters.InputNames.Len())
		},
	},
	BlockSelector: {
		BlockDefinition: BlockDefinition{
			Kind: BlockSelector, Label: "Selector", Category: "Routing",
			Description: "Select a named channel subset", Glyph: "S", Tag: "MIMO ROUTING",
		},
		Defaults: defaultRoutingParameters(),
		Parameters: []parameterDefinition{
			channelNamesField("input_names", "Input channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.InputNames
			}),
			channelNamesField("output_names", "Selected channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.OutputNames
			}),
		},
		portSchema: routingPortSchema,
		realize:    realizeRoutingBlock,
		validate:   validateSelectorParameters,
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("select %d of %d channels", parameters.OutputNames.Len(), parameters.InputNames.Len())
		},
	},
	BlockPermutation: {
		BlockDefinition: BlockDefinition{
			Kind: BlockPermutation, Label: "Permutation", Category: "Routing",
			Description: "Reorder named vector channels", Glyph: "P", Tag: "MIMO ROUTING",
		},
		Defaults: defaultRoutingParameters(),
		Parameters: []parameterDefinition{
			channelNamesField("input_names", "Input channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.InputNames
			}),
			channelNamesField("output_names", "Output order", func(parameters *Parameters) **ChannelNames {
				return &parameters.OutputNames
			}),
		},
		portSchema: routingPortSchema,
		realize:    realizeRoutingBlock,
		validate: func(parameters Parameters) error {
			if err := validateSelectorParameters(parameters); err != nil {
				return err
			}
			if parameters.InputNames.Len() != parameters.OutputNames.Len() {
				return invalid("permutation output must contain every input channel exactly once")
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("reorder %d channels", parameters.InputNames.Len())
		},
	},
	BlockSum: {
		BlockDefinition: BlockDefinition{
			Kind: BlockSum, Label: "Sum", Category: "Math",
			Description: "Signed signal sum", Glyph: "Σ", Tag: "MATH",
		},
		Defaults: Parameters{Signs: "+"},
		Parameters: []parameterDefinition{{
			Name: "signs", Label: "Input signs", Type: "text",
			// The hint states the port rule because the field is now the port
			// list: a wire can only land on a sign, so the old "one sign
			// broadcasts" shorthand would promise inputs Connect refuses.
			Placeholder: "+-", Help: "One sign per input port, in order",
			set: func(parameters *Parameters, raw string) error {
				parameters.Signs = strings.ReplaceAll(strings.TrimSpace(raw), " ", "")
				return nil
			},
			text: func(parameters Parameters) string { return parameters.Signs },
		}},
		variadic: true,
		// Sum's input ports are its signs: one terminal per sign character,
		// so the sign an input carries is the port it lands on and editing
		// the field is how a user adds or removes an input.
		inputPorts: func(parameters Parameters) int { return len(parameters.Signs) },
		declareWiredPorts: func(parameters Parameters, wired int) (Parameters, bool) {
			// Only the one-sign broadcast can widen. With two or more signs
			// every port already has its own and inventing more would guess
			// at a sign the model never stated; beyond maxInputSigns there is
			// no sign string that could name them all, and refusing to widen
			// leaves such a flowsheet computing what it always did.
			if len(parameters.Signs) != 1 || wired > maxInputSigns {
				return parameters, false
			}
			parameters.Signs = strings.Repeat(parameters.Signs, wired)
			return parameters, true
		},
		// realize builds one gain per connected input, taking each input's
		// sign from the port it landed on. That is what makes the sign belong
		// to the terminal: a wire deleted and redrawn onto the same port comes
		// back with the sign it had, whatever order the wires were drawn in.
		// The fallback to the last sign for a port past the end is reached
		// only by a flowsheet an older version wired beyond maxInputSigns —
		// which declareWiredPorts deliberately leaves broadcasting rather than
		// change what it computes, and where a lone sign covers every port.
		// Matching the sign count against the actual connected input count is
		// checkInputs's job, not this hook's.
		realize: func(block Block, ports []int) (*controlsys.System, error) {
			gains := make([]float64, len(ports))
			for i, port := range ports {
				signIndex := min(port, len(block.Parameters.Signs)-1)
				gains[i] = 1
				if block.Parameters.Signs[signIndex] == '-' {
					gains[i] = -1
				}
			}
			return controlsys.NewGain(mat.NewDense(1, len(gains), gains), 0)
		},
		validate: func(parameters Parameters) error {
			if len(parameters.Signs) == 0 || len(parameters.Signs) > maxInputSigns {
				return invalid("input signs must contain 1 to %d plus or minus signs", maxInputSigns)
			}
			for _, sign := range parameters.Signs {
				if sign != '+' && sign != '-' {
					return invalid("input signs may contain only + and -")
				}
			}
			return nil
		},
		// checkInputs is Sum's own rule tying its signs to the connected
		// input count: one sign covers however many wires arrived, otherwise
		// there must be exactly one sign per connection. The first case now
		// only reaches a Sum wired past maxInputSigns, since every other Sum
		// has a sign for each port a wire can reach.
		checkInputs: func(block Block, inputs int) error {
			if len(block.Parameters.Signs) != 1 && len(block.Parameters.Signs) != inputs {
				return invalid("%s has %d input signs for %d connections",
					block.Name, len(block.Parameters.Signs), inputs)
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			return "signs " + parameters.Signs
		},
	},
	BlockVectorSum: {
		BlockDefinition: BlockDefinition{
			Kind: BlockVectorSum, Label: "Vector Sum", Category: "Math",
			Description: "Signed sum of named vectors", Glyph: "Σv", Tag: "MIMO",
		},
		Defaults: defaultVectorSumParameters(),
		Parameters: []parameterDefinition{
			{
				Name: "signs", Label: "Input signs", Type: "text",
				Placeholder: "+-", Help: "One sign per vector input port, in order",
				set: func(parameters *Parameters, raw string) error {
					parameters.Signs = strings.ReplaceAll(strings.TrimSpace(raw), " ", "")
					return nil
				},
				text: func(parameters Parameters) string { return parameters.Signs },
			},
			channelNamesField("input_names", "Input channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.InputNames
			}),
			channelNamesField("output_names", "Output channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.OutputNames
			}),
		},
		variadic:   true,
		inputPorts: func(parameters Parameters) int { return len(parameters.Signs) },
		declareWiredPorts: func(parameters Parameters, wired int) (Parameters, bool) {
			if len(parameters.Signs) != 1 || wired > maxInputSigns {
				return parameters, false
			}
			parameters.Signs = strings.Repeat(parameters.Signs, wired)
			return parameters, true
		},
		portSchema: func(parameters Parameters) blockPortSchema {
			if parameters.InputNames == nil || parameters.OutputNames == nil {
				return blockPortSchema{}
			}
			inputs := make([]SignalPort, len(parameters.Signs))
			for port := range inputs {
				inputs[port], _ = newSignalPort(
					parameters.InputNames.Len(), parameters.InputNames.Names(),
				)
			}
			output, _ := newSignalPort(
				parameters.OutputNames.Len(), parameters.OutputNames.Names(),
			)
			return blockPortSchema{inputs: inputs, outputs: []SignalPort{output}}
		},
		realize: func(block Block, ports []int) (*controlsys.System, error) {
			width := block.Parameters.InputNames.Len()
			values := make([]float64, width*width*len(ports))
			for inputIndex, port := range ports {
				signIndex := min(port, len(block.Parameters.Signs)-1)
				gain := 1.0
				if block.Parameters.Signs[signIndex] == '-' {
					gain = -1
				}
				for channel := range width {
					values[channel*(width*len(ports))+inputIndex*width+channel] = gain
				}
			}
			return controlsys.NewGain(
				mat.NewDense(width, width*len(ports), values),
				0,
			)
		},
		validate: func(parameters Parameters) error {
			if len(parameters.Signs) == 0 || len(parameters.Signs) > maxInputSigns {
				return invalid("input signs must contain 1 to %d plus or minus signs", maxInputSigns)
			}
			for _, sign := range parameters.Signs {
				if sign != '+' && sign != '-' {
					return invalid("input signs may contain only + and -")
				}
			}
			if parameters.InputNames == nil || parameters.OutputNames == nil {
				return invalid("input and output channel names are required")
			}
			if parameters.InputNames.Len() != parameters.OutputNames.Len() {
				return invalid(
					"vector sum has %d input channels but %d output channels",
					parameters.InputNames.Len(), parameters.OutputNames.Len(),
				)
			}
			return nil
		},
		checkInputs: func(block Block, inputs int) error {
			if len(block.Parameters.Signs) != 1 && len(block.Parameters.Signs) != inputs {
				return invalid("%s has %d input signs for %d connections",
					block.Name, len(block.Parameters.Signs), inputs)
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("%d-channel signs %s", parameters.InputNames.Len(), parameters.Signs)
		},
	},
	BlockLag: {
		BlockDefinition: BlockDefinition{
			Kind: BlockLag, Label: "First-order Lag", Category: "Continuous",
			Description: "1 / (τs + 1)", Glyph: "τ", Tag: "CONTINUOUS",
		},
		Defaults: Parameters{TimeConstant: 1},
		Parameters: []parameterDefinition{
			numberField("time_constant", "Time constant", "time constant", "0.001", 0.001, 1000, "sec", func(p *Parameters) *float64 { return &p.TimeConstant }),
		},
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			tau := block.Parameters.TimeConstant
			return controlsys.New(
				mat.NewDense(1, 1, []float64{-1 / tau}),
				mat.NewDense(1, 1, []float64{1 / tau}),
				mat.NewDense(1, 1, []float64{1}),
				mat.NewDense(1, 1, []float64{0}),
				0,
			)
		},
		timeDomain: func(Parameters) blockTimeDomain { return continuousTimeDomain() },
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("τ = %.3g s", parameters.TimeConstant)
		},
	},
	BlockIntegrator: {
		BlockDefinition: BlockDefinition{
			Kind: BlockIntegrator, Label: "Integrator", Category: "Continuous",
			Description: "Continuous 1 / s", Glyph: "∫", Tag: "CONTINUOUS",
		},
		realize: func(Block, []int) (*controlsys.System, error) {
			return controlsys.New(
				mat.NewDense(1, 1, []float64{0}),
				mat.NewDense(1, 1, []float64{1}),
				mat.NewDense(1, 1, []float64{1}),
				mat.NewDense(1, 1, []float64{0}),
				0,
			)
		},
		timeDomain: func(Parameters) blockTimeDomain { return continuousTimeDomain() },
		summary:    func(Parameters) string { return "1 / s" },
	},
	BlockTransfer: {
		BlockDefinition: BlockDefinition{
			Kind: BlockTransfer, Label: "Transfer Function", Category: "Continuous",
			Description: "Proper SISO model", Glyph: "G", Tag: "CONTINUOUS",
		},
		Defaults: Parameters{Numerator: []float64{1}, Denominator: []float64{1, 1}},
		Parameters: []parameterDefinition{
			coefficientField("numerator", "Numerator coefficients", "1, 3", func(p *Parameters) *[]float64 { return &p.Numerator }),
			coefficientField("denominator", "Denominator coefficients", "1, 2, 1", func(p *Parameters) *[]float64 { return &p.Denominator }),
		},
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			result, err := (&controlsys.TransferFunc{
				Num: [][][]float64{{append([]float64(nil), block.Parameters.Numerator...)}},
				Den: [][]float64{append([]float64(nil), block.Parameters.Denominator...)},
			}).StateSpace(nil)
			if err != nil {
				return nil, err
			}
			return result.Sys, nil
		},
		timeDomain: func(Parameters) blockTimeDomain { return continuousTimeDomain() },
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
			Kind: BlockPID, Label: "PID Controller", Category: "Control",
			Description: "Filtered parallel PID", Glyph: "PID", Tag: "CONTROL",
		},
		Defaults: Parameters{
			Proportional: 1, Integral: 0.5, FilterTime: 0.1,
			TimeDomain: modelDomainContinuous, SampleTime: 0.1,
		},
		Parameters: append([]parameterDefinition{
			numberField("proportional", "Proportional Kp", "proportional gain", "any", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Proportional }),
			numberField("integral", "Integral Ki", "integral gain", "any", -10000, 10000, "1/sec", func(p *Parameters) *float64 { return &p.Integral }),
			numberField("derivative", "Derivative Kd", "derivative gain", "any", -10000, 10000, "sec", func(p *Parameters) *float64 { return &p.Derivative }),
			numberField("filter_time", "Derivative filter Tf", "derivative filter", "0.001", 0.001, 1000, "sec", func(p *Parameters) *float64 { return &p.FilterTime }),
		}, representationTimeFields()...),
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			return controlsys.NewPID(
				block.Parameters.Proportional,
				block.Parameters.Integral,
				block.Parameters.Derivative,
				controlsys.WithFilter(block.Parameters.FilterTime),
				controlsys.WithTs(representationSampleTime(block.Parameters)),
			).System()
		},
		timeDomain: representationTimeDomain,
		validate: func(parameters Parameters) error {
			if err := validateRepresentationTime(parameters); err != nil {
				return err
			}
			_, err := controlsys.NewPID(
				parameters.Proportional,
				parameters.Integral,
				parameters.Derivative,
				controlsys.WithFilter(parameters.FilterTime),
				controlsys.WithTs(representationSampleTime(parameters)),
			).System()
			if err != nil {
				return invalid("PID realization: %s", err)
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("P %.3g · I %.3g · D %.3g · %s",
				parameters.Proportional, parameters.Integral, parameters.Derivative,
				normalizedModelDomain(parameters))
		},
	},
	BlockPID2: {
		BlockDefinition: BlockDefinition{
			Kind: BlockPID2, Label: "2-DOF PID Controller", Category: "Control",
			Description: "Reference-weighted filtered parallel PID", Glyph: "PID2", Tag: "CONTROL",
		},
		Defaults: Parameters{
			Proportional: 1, Integral: 0.5, FilterTime: 0.1,
			SetpointWeight: 1, DerivativeWeight: 1,
			TimeDomain: modelDomainContinuous, SampleTime: 0.1,
		},
		Parameters: append([]parameterDefinition{
			numberField("proportional", "Proportional Kp", "proportional gain", "any", -10000, 10000, "scalar", func(p *Parameters) *float64 { return &p.Proportional }),
			numberField("integral", "Integral Ki", "integral gain", "any", -10000, 10000, "1/sec", func(p *Parameters) *float64 { return &p.Integral }),
			numberField("derivative", "Derivative Kd", "derivative gain", "any", -10000, 10000, "sec", func(p *Parameters) *float64 { return &p.Derivative }),
			numberField("filter_time", "Derivative filter Tf", "derivative filter", "0.001", 0.001, 1000, "sec", func(p *Parameters) *float64 { return &p.FilterTime }),
			numberField("setpoint_weight", "Setpoint weight b", "setpoint weight", "any", -10, 10, "scalar", func(p *Parameters) *float64 { return &p.SetpointWeight }),
			numberField("derivative_weight", "Derivative weight c", "derivative weight", "any", -10, 10, "scalar", func(p *Parameters) *float64 { return &p.DerivativeWeight }),
		}, representationTimeFields()...),
		variadic:   true,
		inputPorts: func(Parameters) int { return 2 },
		portSchema: func(Parameters) blockPortSchema {
			reference, _ := newSignalPort(1, []string{"reference"})
			measurement, _ := newSignalPort(1, []string{"measurement"})
			control, _ := newSignalPort(1, []string{"control"})
			return blockPortSchema{
				inputs:  []SignalPort{reference, measurement},
				outputs: []SignalPort{control},
			}
		},
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			return controlsys.NewPID2(
				block.Parameters.Proportional,
				block.Parameters.Integral,
				block.Parameters.Derivative,
				block.Parameters.FilterTime,
				block.Parameters.SetpointWeight,
				block.Parameters.DerivativeWeight,
				controlsys.WithTs(representationSampleTime(block.Parameters)),
			).System()
		},
		timeDomain: representationTimeDomain,
		validate: func(parameters Parameters) error {
			if err := validateRepresentationTime(parameters); err != nil {
				return err
			}
			_, err := controlsys.NewPID2(
				parameters.Proportional,
				parameters.Integral,
				parameters.Derivative,
				parameters.FilterTime,
				parameters.SetpointWeight,
				parameters.DerivativeWeight,
				controlsys.WithTs(representationSampleTime(parameters)),
			).System()
			if err != nil {
				return invalid("PID2 realization: %s", err)
			}
			return nil
		},
		checkInputs: func(block Block, inputs int) error {
			if inputs != 2 {
				return invalid("%s requires reference and measurement inputs", block.Name)
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf(
				"P %.3g · I %.3g · D %.3g · b %.3g · c %.3g · %s",
				parameters.Proportional, parameters.Integral, parameters.Derivative,
				parameters.SetpointWeight, parameters.DerivativeWeight,
				normalizedModelDomain(parameters),
			)
		},
	},
	BlockDelay: {
		BlockDefinition: BlockDefinition{
			Kind: BlockDelay, Label: "Transport Delay", Category: "Continuous",
			Description: "Exact delay with explicit Padé and Thiran approximations", Glyph: "e⁻ˢ", Tag: "DELAY",
		},
		Defaults: Parameters{
			Delay: 1, DelayMode: delayModeExact, Approximation: 3,
			SampleTime: 0.1, SampleTimeMode: string(sampleTimeExplicit),
		},
		Parameters: []parameterDefinition{
			numberField("delay", "Delay", "delay", "0.05", 0, 120, "sec", func(p *Parameters) *float64 { return &p.Delay }),
			{
				Name: "delay_mode", Label: "Representation", Type: "select",
				Options: []parameterOption{
					{Value: delayModeExact, Label: "Exact transport delay"},
					{Value: delayModePade, Label: "Padé (continuous)"},
					{Value: delayModeThiran, Label: "Thiran (discrete)"},
				},
				set: func(parameters *Parameters, raw string) error {
					parameters.DelayMode = strings.ToLower(strings.TrimSpace(raw))
					return nil
				},
				text: func(parameters Parameters) string { return normalizedDelayMode(parameters) },
				Help: "Exact preserves delay metadata. Padé and Thiran are explicit finite-order approximations.",
			},
			{
				Name: "approximation", Label: "Approximation order", Type: "number",
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
				Help: "Used only by Padé and Thiran representations.",
			},
			{
				Name: "sample_time_mode", Label: "Sample time source", Type: "select",
				Options: []parameterOption{
					{Value: string(sampleTimeExplicit), Label: "Explicit"},
					{Value: string(sampleTimeInherited), Label: "Inherit run sample time"},
				},
				set: func(parameters *Parameters, raw string) error {
					parameters.SampleTimeMode = strings.ToLower(strings.TrimSpace(raw))
					return nil
				},
				text: func(parameters Parameters) string {
					return string(normalizedSampleTimeMode(parameters))
				},
				Help: "Used by the discrete Thiran representation.",
			},
			conditionalNumberField(
				"sample_time", "Approximation sample time", "sample time",
				"0.001", 0.001, 10, "sec",
				func(p *Parameters) *float64 { return &p.SampleTime },
				func(parameters Parameters) bool {
					return normalizedDelayMode(parameters) == delayModeThiran &&
						normalizedSampleTimeMode(parameters) == sampleTimeExplicit
				},
			),
		},
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			switch normalizedDelayMode(block.Parameters) {
			case delayModeExact:
				system, err := controlsys.NewGain(mat.NewDense(1, 1, []float64{1}), 0)
				if err != nil {
					return nil, err
				}
				if err := system.SetInputDelay([]float64{block.Parameters.Delay}); err != nil {
					return nil, err
				}
				return system, nil
			case delayModePade:
				return controlsys.PadeDelay(block.Parameters.Delay, block.Parameters.Approximation)
			case delayModeThiran:
				if normalizedSampleTimeMode(block.Parameters) == sampleTimeInherited {
					return nil, invalid("inherited Thiran sample time must be resolved from the run sample time")
				}
				return controlsys.ThiranDelay(
					block.Parameters.Delay,
					block.Parameters.Approximation,
					block.Parameters.SampleTime,
				)
			default:
				return nil, invalid("delay representation must be exact, Padé, or Thiran")
			}
		},
		timeDomain: func(parameters Parameters) blockTimeDomain {
			if normalizedDelayMode(parameters) == delayModeThiran {
				return discreteTimeDomain(parameters)
			}
			return continuousTimeDomain()
		},
		validate: func(parameters Parameters) error {
			mode := normalizedDelayMode(parameters)
			if mode != delayModeExact && mode != delayModePade && mode != delayModeThiran {
				return invalid("delay representation must be exact, Padé, or Thiran")
			}
			sampleMode := normalizedSampleTimeMode(parameters)
			if sampleMode != sampleTimeExplicit && sampleMode != sampleTimeInherited {
				return invalid("sample time mode must be explicit or inherited")
			}
			if mode == delayModeExact {
				return nil
			}
			if parameters.Approximation < minApproximation || parameters.Approximation > maxApproximation {
				return invalid("approximation order must be between %d and %d", minApproximation, maxApproximation)
			}
			if mode == delayModeThiran {
				if sampleMode == sampleTimeExplicit {
					samples := parameters.Delay / parameters.SampleTime
					minimum := float64(parameters.Approximation) - 0.5
					if samples < minimum {
						return invalid(
							"Thiran delay must be at least %.1f samples for order %d; increase delay, reduce order, or reduce sample time",
							minimum, parameters.Approximation,
						)
					}
				}
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			switch normalizedDelayMode(parameters) {
			case delayModeExact:
				return fmt.Sprintf("%.3g s · exact", parameters.Delay)
			case delayModeThiran:
				if normalizedSampleTimeMode(parameters) == sampleTimeInherited {
					return fmt.Sprintf("%.3g s · Thiran %d @ run step", parameters.Delay, parameters.Approximation)
				}
				return fmt.Sprintf("%.3g s · Thiran %d @ %.3g s", parameters.Delay, parameters.Approximation, parameters.SampleTime)
			default:
				return fmt.Sprintf("%.3g s · Padé %d", parameters.Delay, parameters.Approximation)
			}
		},
	},
	BlockStateSpace: {
		BlockDefinition: BlockDefinition{
			Kind: BlockStateSpace, Label: "State-Space", Category: "Models",
			Description: "Named continuous or discrete MIMO model", Glyph: "SS", Tag: "MIMO",
		},
		Defaults: defaultStateSpaceParameters(),
		Parameters: append([]parameterDefinition{
			matrixField("a", "A matrix", func(parameters *Parameters) **MatrixValue { return &parameters.A }),
			matrixField("b", "B matrix", func(parameters *Parameters) **MatrixValue { return &parameters.B }),
			matrixField("c", "C matrix", func(parameters *Parameters) **MatrixValue { return &parameters.C }),
			matrixField("d", "D matrix", func(parameters *Parameters) **MatrixValue { return &parameters.D }),
			channelNamesField("input_names", "Input channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.InputNames
			}),
			channelNamesField("output_names", "Output channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.OutputNames
			}),
			channelNamesField("state_names", "State names", func(parameters *Parameters) **ChannelNames {
				return &parameters.StateNames
			}),
		}, representationTimeFields()...),
		portSchema: namedLTIPortSchema,
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			system, err := stateSpaceFromParameters(block.Parameters)
			if err != nil {
				return nil, fmt.Errorf("controlsys state-space construction: %w", err)
			}
			return system, nil
		},
		timeDomain: representationTimeDomain,
		validate:   validateStateSpaceParameters,
		summary: func(parameters Parameters) string {
			states, _ := parameters.A.Dims()
			return fmt.Sprintf(
				"%d-state %d×%d · %s",
				states, parameters.OutputNames.Len(), parameters.InputNames.Len(),
				normalizedModelDomain(parameters),
			)
		},
	},
	BlockMIMOTransfer: {
		BlockDefinition: BlockDefinition{
			Kind: BlockMIMOTransfer, Label: "MIMO Transfer Function", Category: "Models",
			Description: "Named transfer matrix with row denominators and pairwise delays",
			Glyph:       "G(s)", Tag: "MIMO",
		},
		Defaults: defaultMIMOTransferParameters(),
		Parameters: append([]parameterDefinition{
			polynomialMatrixField(
				"transfer_numerators", "Numerator matrix",
				func(parameters *Parameters) **PolynomialMatrixValue {
					return &parameters.TransferNumerators
				},
			),
			polynomialMatrixField(
				"transfer_denominators", "Denominator rows",
				func(parameters *Parameters) **PolynomialMatrixValue {
					return &parameters.TransferDenominators
				},
			),
			matrixField("transfer_delays", "Pairwise delays", func(parameters *Parameters) **MatrixValue {
				return &parameters.TransferDelays
			}),
			channelNamesField("input_names", "Input channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.InputNames
			}),
			channelNamesField("output_names", "Output channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.OutputNames
			}),
		}, representationTimeFields()...),
		portSchema: namedLTIPortSchema,
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			system, err := transferSystemFromParameters(block.Parameters)
			if err != nil {
				return nil, fmt.Errorf("controlsys transfer conversion: %w", err)
			}
			return system, nil
		},
		timeDomain: representationTimeDomain,
		validate:   validateMIMOTransferParameters,
		summary: func(parameters Parameters) string {
			outputs, inputs := parameters.TransferNumerators.Dims()
			return fmt.Sprintf("%d×%d transfer matrix · %s", outputs, inputs, normalizedModelDomain(parameters))
		},
	},
	BlockZPK: {
		BlockDefinition: BlockDefinition{
			Kind: BlockZPK, Label: "Zero-Pole-Gain", Category: "Models",
			Description: "Named MIMO zero-pole-gain model", Glyph: "ZPK", Tag: "MIMO",
		},
		Defaults: defaultZPKParameters(),
		Parameters: append([]parameterDefinition{
			complexRootMatrixField(
				"zeros", "Zero matrix", func(parameters *Parameters) **ComplexRootMatrixValue {
					return &parameters.Zeros
				},
			),
			complexRootMatrixField(
				"poles", "Pole matrix", func(parameters *Parameters) **ComplexRootMatrixValue {
					return &parameters.Poles
				},
			),
			matrixField("d", "Gain matrix", func(parameters *Parameters) **MatrixValue {
				return &parameters.D
			}),
			channelNamesField("input_names", "Input channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.InputNames
			}),
			channelNamesField("output_names", "Output channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.OutputNames
			}),
		}, representationTimeFields()...),
		portSchema: namedLTIPortSchema,
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			system, err := zpkSystemFromParameters(block.Parameters)
			if err != nil {
				return nil, fmt.Errorf("controlsys ZPK conversion: %w", err)
			}
			return system, nil
		},
		timeDomain: representationTimeDomain,
		validate:   validateZPKParameters,
		summary: func(parameters Parameters) string {
			outputs, inputs := parameters.D.Dims()
			return fmt.Sprintf("%d×%d zero-pole-gain · %s", outputs, inputs, normalizedModelDomain(parameters))
		},
	},
	BlockFRD: {
		BlockDefinition: BlockDefinition{
			Kind: BlockFRD, Label: "Frequency Response Data", Category: "Models",
			Description: "Named complex MIMO samples for frequency-domain workflows",
			Glyph:       "FRD", Tag: "FREQUENCY",
		},
		Defaults: defaultFRDParameters(),
		Parameters: append([]parameterDefinition{
			channelNamesField("input_names", "Input channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.InputNames
			}),
			channelNamesField("output_names", "Output channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.OutputNames
			}),
			vectorField("frequencies", "Frequency grid", func(parameters *Parameters) **VectorValue {
				return &parameters.Frequencies
			}),
			complexResponseField(
				"frequency_response", "Complex response samples",
				func(parameters *Parameters) **ComplexResponseValue {
					return &parameters.FrequencyResponse
				},
			),
			{
				Name: "frequency_unit", Label: "Frequency unit", Type: "select",
				Options: []parameterOption{
					{Value: frequencyUnitRadiansPerSecond, Label: "Radians per second"},
				},
				set: func(parameters *Parameters, raw string) error {
					parameters.FrequencyUnit = strings.TrimSpace(raw)
					return nil
				},
				text: func(parameters Parameters) string { return parameters.FrequencyUnit },
			},
			{
				Name: "response_unit", Label: "Response unit", Type: "select",
				Options: []parameterOption{
					{Value: responseUnitLinearComplexGain, Label: "Linear complex gain"},
				},
				set: func(parameters *Parameters, raw string) error {
					parameters.ResponseUnit = strings.TrimSpace(raw)
					return nil
				},
				text: func(parameters Parameters) string { return parameters.ResponseUnit },
			},
		}, representationTimeFields()...),
		portSchema: namedLTIPortSchema,
		realize:    realizeFRDBlock,
		timeDomain: representationTimeDomain,
		validate:   validateFRDParameters,
		summary: func(parameters Parameters) string {
			samples, outputs, inputs := parameters.FrequencyResponse.Dims()
			return fmt.Sprintf("%d×%d · %d frequencies", outputs, inputs, samples)
		},
	},
	BlockUnitDelay: {
		BlockDefinition: BlockDefinition{
			Kind: BlockUnitDelay, Label: "Unit Delay", Category: "Discrete",
			Description: "Exact one-sample memory", Glyph: "z⁻¹", Tag: "DISCRETE",
		},
		Defaults: Parameters{
			SampleTime: 0.1, SampleTimeMode: string(sampleTimeExplicit),
		},
		Parameters: sampleTimeFields(),
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			return controlsys.New(
				mat.NewDense(1, 1, []float64{0}),
				mat.NewDense(1, 1, []float64{1}),
				mat.NewDense(1, 1, []float64{1}),
				mat.NewDense(1, 1, []float64{0}),
				block.Parameters.SampleTime,
			)
		},
		timeDomain: func(parameters Parameters) blockTimeDomain {
			return discreteTimeDomain(parameters)
		},
		validate: validateDiscreteSampleTime,
		summary: func(parameters Parameters) string {
			if normalizedSampleTimeMode(parameters) == sampleTimeInherited {
				return "z⁻¹ @ run step"
			}
			return fmt.Sprintf("z⁻¹ @ %.3g s", parameters.SampleTime)
		},
	},
	BlockDiscreteTransfer: {
		BlockDefinition: BlockDefinition{
			Kind: BlockDiscreteTransfer, Label: "Discrete Transfer Function", Category: "Discrete",
			Description: "Proper SISO model in z", Glyph: "H(z)", Tag: "DISCRETE",
		},
		Defaults: Parameters{
			Numerator: []float64{0.1}, Denominator: []float64{1, -0.9},
			SampleTime: 0.1, SampleTimeMode: string(sampleTimeExplicit),
		},
		Parameters: append([]parameterDefinition{
			coefficientField("numerator", "Numerator coefficients", "0.1", func(parameters *Parameters) *[]float64 {
				return &parameters.Numerator
			}),
			coefficientField("denominator", "Denominator coefficients", "1, -0.9", func(parameters *Parameters) *[]float64 {
				return &parameters.Denominator
			}),
		}, sampleTimeFields()...),
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			result, err := (&controlsys.TransferFunc{
				Num: [][][]float64{{append([]float64(nil), block.Parameters.Numerator...)}},
				Den: [][]float64{append([]float64(nil), block.Parameters.Denominator...)},
				Dt:  block.Parameters.SampleTime,
			}).StateSpace(nil)
			if err != nil {
				return nil, err
			}
			return result.Sys, nil
		},
		timeDomain: func(parameters Parameters) blockTimeDomain {
			return discreteTimeDomain(parameters)
		},
		validate: func(parameters Parameters) error {
			if err := validateDiscreteSampleTime(parameters); err != nil {
				return err
			}
			if len(parameters.Numerator) == 0 || len(parameters.Denominator) == 0 {
				return invalid("transfer function coefficients are required")
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
			return polynomialText(parameters.Numerator) + " / " +
				polynomialText(parameters.Denominator) + " in z"
		},
	},
	BlockDiscreteStateSpace: {
		BlockDefinition: BlockDefinition{
			Kind: BlockDiscreteStateSpace, Label: "Discrete State-Space", Category: "Discrete",
			Description: "Named x[k+1]=Ax+Bu, y=Cx+Du", Glyph: "SSz", Tag: "MIMO",
		},
		Defaults: defaultDiscreteStateSpaceParameters(),
		Parameters: append([]parameterDefinition{
			matrixField("a", "A matrix", func(parameters *Parameters) **MatrixValue { return &parameters.A }),
			matrixField("b", "B matrix", func(parameters *Parameters) **MatrixValue { return &parameters.B }),
			matrixField("c", "C matrix", func(parameters *Parameters) **MatrixValue { return &parameters.C }),
			matrixField("d", "D matrix", func(parameters *Parameters) **MatrixValue { return &parameters.D }),
			channelNamesField("input_names", "Input channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.InputNames
			}),
			channelNamesField("output_names", "Output channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.OutputNames
			}),
			channelNamesField("state_names", "State names", func(parameters *Parameters) **ChannelNames {
				return &parameters.StateNames
			}),
		}, sampleTimeFields()...),
		portSchema: func(parameters Parameters) blockPortSchema {
			if parameters.B == nil || parameters.C == nil ||
				parameters.InputNames == nil || parameters.OutputNames == nil {
				return blockPortSchema{}
			}
			_, inputs := parameters.B.Dims()
			outputs, _ := parameters.C.Dims()
			input, _ := newSignalPort(inputs, parameters.InputNames.Names())
			output, _ := newSignalPort(outputs, parameters.OutputNames.Names())
			return blockPortSchema{
				inputs: []SignalPort{input}, outputs: []SignalPort{output},
			}
		},
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			n, _ := block.Parameters.A.Dims()
			_, m := block.Parameters.B.Dims()
			p, _ := block.Parameters.C.Dims()
			system, err := controlsys.New(
				mat.NewDense(n, n, block.Parameters.A.Values()),
				mat.NewDense(n, m, block.Parameters.B.Values()),
				mat.NewDense(p, n, block.Parameters.C.Values()),
				mat.NewDense(p, m, block.Parameters.D.Values()),
				block.Parameters.SampleTime,
			)
			if err != nil {
				return nil, err
			}
			system.StateName = block.Parameters.StateNames.Names()
			return system, nil
		},
		timeDomain: func(parameters Parameters) blockTimeDomain {
			return discreteTimeDomain(parameters)
		},
		validate: validateDiscreteStateSpaceParameters,
		summary: func(parameters Parameters) string {
			if parameters.A == nil || parameters.B == nil || parameters.C == nil {
				return "invalid state-space"
			}
			states, _ := parameters.A.Dims()
			_, inputs := parameters.B.Dims()
			outputs, _ := parameters.C.Dims()
			return fmt.Sprintf("%d states · %d×%d I/O", states, outputs, inputs)
		},
	},
	BlockDiscretizedTransfer: {
		BlockDefinition: BlockDefinition{
			Kind: BlockDiscretizedTransfer, Label: "Discretized Transfer", Category: "Discrete",
			Description: "Explicit continuous-to-discrete conversion", Glyph: "c2d", Tag: "CONVERSION",
		},
		Defaults: Parameters{
			Numerator: []float64{1}, Denominator: []float64{1, 1},
			SampleTime: 0.1, SampleTimeMode: string(sampleTimeExplicit),
			ConversionMethod: string(controlsys.C2DMethodZOH),
		},
		Parameters: append([]parameterDefinition{
			coefficientField("numerator", "Continuous numerator", "1", func(parameters *Parameters) *[]float64 {
				return &parameters.Numerator
			}),
			coefficientField("denominator", "Continuous denominator", "1, 1", func(parameters *Parameters) *[]float64 {
				return &parameters.Denominator
			}),
			{
				Name: "conversion_method", Label: "Conversion method", Type: "select",
				Options: []parameterOption{
					{Value: string(controlsys.C2DMethodZOH), Label: "Zero-order hold"},
					{Value: string(controlsys.C2DMethodFOH), Label: "First-order hold"},
					{Value: string(controlsys.C2DMethodMatched), Label: "Matched pole-zero"},
					{Value: string(controlsys.C2DMethodImpulse), Label: "Impulse invariant"},
				},
				set: func(parameters *Parameters, raw string) error {
					parameters.ConversionMethod = strings.ToLower(strings.TrimSpace(raw))
					return nil
				},
				text: func(parameters Parameters) string { return parameters.ConversionMethod },
			},
		}, sampleTimeFields()...),
		realize: realizeDiscretizedTransfer,
		timeDomain: func(parameters Parameters) blockTimeDomain {
			return discreteTimeDomain(parameters)
		},
		validate: validateDiscretizedTransferParameters,
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("%s @ %.3g s", parameters.ConversionMethod, parameters.SampleTime)
		},
	},
	BlockScope: {
		BlockDefinition: BlockDefinition{
			Kind: BlockScope, Label: "Scope", Category: "Sinks",
			Description: "Plot a signal", Glyph: "⌁", Tag: "OUTPUT",
		},
		role:    roleSink,
		summary: func(Parameters) string { return "trend output" },
	},
	BlockVectorScope: {
		BlockDefinition: BlockDefinition{
			Kind: BlockVectorScope, Label: "Vector Scope", Category: "Sinks",
			Description: "Plot named vector channels", Glyph: "⌁v", Tag: "MIMO OUTPUT",
		},
		Defaults: defaultVectorScopeParameters(),
		Parameters: []parameterDefinition{
			channelNamesField("input_names", "Input channels", func(parameters *Parameters) **ChannelNames {
				return &parameters.InputNames
			}),
		},
		role: roleSink,
		portSchema: func(parameters Parameters) blockPortSchema {
			if parameters.InputNames == nil {
				return blockPortSchema{}
			}
			input, _ := newSignalPort(parameters.InputNames.Len(), parameters.InputNames.Names())
			return blockPortSchema{inputs: []SignalPort{input}}
		},
		realize: func(block Block, _ []int) (*controlsys.System, error) {
			return controlsys.NewGain(identityDense(block.Parameters.InputNames.Len()), 0)
		},
		validate: func(parameters Parameters) error {
			if parameters.InputNames == nil {
				return invalid("input channel names are required")
			}
			return nil
		},
		summary: func(parameters Parameters) string {
			return fmt.Sprintf("%d-channel trend output", parameters.InputNames.Len())
		},
	},
	BlockSpectrum: {
		BlockDefinition: BlockDefinition{
			Kind: BlockSpectrum, Label: "Spectrum Analyzer", Category: "Sinks",
			Description: "Hann-windowed FFT", Glyph: "FFT", Tag: "DSP SINK",
		},
		role:     roleSink,
		spectrum: true,
		summary:  func(Parameters) string { return "frequency output" },
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

func conditionalNumberField(
	name, label, boundsLabel, step string,
	minimum, maximum float64,
	unit string,
	field func(*Parameters) *float64,
	active func(Parameters) bool,
) parameterDefinition {
	definition := numberField(
		name, label, boundsLabel, step, minimum, maximum, unit, field,
	)
	definition.active = active
	return definition
}

func sampleTimeFields() []parameterDefinition {
	return []parameterDefinition{
		{
			Name: "sample_time_mode", Label: "Sample time source", Type: "select",
			Options: []parameterOption{
				{Value: string(sampleTimeExplicit), Label: "Explicit"},
				{Value: string(sampleTimeInherited), Label: "Inherit run sample time"},
			},
			set: func(parameters *Parameters, raw string) error {
				parameters.SampleTimeMode = strings.ToLower(strings.TrimSpace(raw))
				return nil
			},
			text: func(parameters Parameters) string {
				return string(normalizedSampleTimeMode(parameters))
			},
		},
		conditionalNumberField(
			"sample_time", "Sample time", "sample time",
			"0.001", 0.001, 10, "sec",
			func(parameters *Parameters) *float64 { return &parameters.SampleTime },
			func(parameters Parameters) bool {
				return normalizedSampleTimeMode(parameters) == sampleTimeExplicit
			},
		),
	}
}

func validateDiscreteSampleTime(parameters Parameters) error {
	mode := normalizedSampleTimeMode(parameters)
	if mode != sampleTimeExplicit && mode != sampleTimeInherited {
		return invalid("sample time mode must be explicit or inherited")
	}
	return nil
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
			value, err := ParseVectorValue(raw)
			if err != nil {
				return invalid("%s coefficients must be comma or space separated numbers", name)
			}
			*field(parameters) = value.Values()
			return nil
		},
		text: func(parameters Parameters) string {
			value, err := NewVectorValue(*field(&parameters))
			if err != nil {
				return ""
			}
			return value.Text()
		},
		shape: func(parameters Parameters) (int, int) {
			return 1, len(*field(&parameters))
		},
	}
}

func matrixField(
	name, label string,
	field func(*Parameters) **MatrixValue,
) parameterDefinition {
	return parameterDefinition{
		Name: name, Label: label, Type: "textarea",
		Placeholder: "1, 0\n0, 1",
		Help:        "Rows are separated by a new line or semicolon.",
		set: func(parameters *Parameters, raw string) error {
			value, err := ParseMatrixValue(raw)
			if err != nil {
				return err
			}
			*field(parameters) = &value
			return nil
		},
		text: func(parameters Parameters) string {
			value := *field(&parameters)
			if value == nil {
				return ""
			}
			return value.Text()
		},
		shape: func(parameters Parameters) (int, int) {
			value := *field(&parameters)
			if value == nil {
				return 0, 0
			}
			return value.Dims()
		},
	}
}

func polynomialMatrixField(
	name, label string,
	field func(*Parameters) **PolynomialMatrixValue,
) parameterDefinition {
	return parameterDefinition{
		Name: name, Label: label, Type: "textarea",
		Placeholder: "1 | 0\n0 | 1",
		Help:        "Rows use new lines, channels use |, coefficients use commas in descending powers.",
		set: func(parameters *Parameters, raw string) error {
			value, err := ParsePolynomialMatrixValue(raw)
			if err != nil {
				return err
			}
			*field(parameters) = &value
			return nil
		},
		text: func(parameters Parameters) string {
			value := *field(&parameters)
			if value == nil {
				return ""
			}
			return value.Text()
		},
		shape: func(parameters Parameters) (int, int) {
			value := *field(&parameters)
			if value == nil {
				return 0, 0
			}
			return value.Dims()
		},
	}
}

func complexRootMatrixField(
	name, label string,
	field func(*Parameters) **ComplexRootMatrixValue,
) parameterDefinition {
	return parameterDefinition{
		Name: name, Label: label, Type: "textarea",
		Placeholder: "-1+2i, -1-2i | -",
		Help:        "Rows use new lines, channels use |, roots use commas, and - means no roots.",
		set: func(parameters *Parameters, raw string) error {
			value, err := ParseComplexRootMatrixValue(raw)
			if err != nil {
				return err
			}
			*field(parameters) = &value
			return nil
		},
		text: func(parameters Parameters) string {
			value := *field(&parameters)
			if value == nil {
				return ""
			}
			return value.Text()
		},
		shape: func(parameters Parameters) (int, int) {
			value := *field(&parameters)
			if value == nil {
				return 0, 0
			}
			return value.Dims()
		},
	}
}

func complexResponseField(
	name, label string,
	field func(*Parameters) **ComplexResponseValue,
) parameterDefinition {
	return parameterDefinition{
		Name: name, Label: label, Type: "textarea",
		Placeholder: "1-0.1i | 0 | 0 | 0.5-0.02i",
		Help:        "One frequency per row; response channels are row-major output-by-input values separated by |.",
		set: func(parameters *Parameters, raw string) error {
			if parameters.InputNames == nil || parameters.OutputNames == nil {
				return invalid("input and output channel names are required before frequency responses")
			}
			value, err := ParseComplexResponseValue(
				raw, parameters.OutputNames.Len(), parameters.InputNames.Len(),
			)
			if err != nil {
				return err
			}
			*field(parameters) = &value
			return nil
		},
		text: func(parameters Parameters) string {
			value := *field(&parameters)
			if value == nil {
				return ""
			}
			return value.Text()
		},
		shape: func(parameters Parameters) (int, int) {
			value := *field(&parameters)
			if value == nil {
				return 0, 0
			}
			samples, outputs, inputs := value.Dims()
			return samples, outputs * inputs
		},
	}
}

func vectorField(
	name, label string,
	field func(*Parameters) **VectorValue,
) parameterDefinition {
	return parameterDefinition{
		Name: name, Label: label, Type: "text",
		Placeholder: "1, 0",
		set: func(parameters *Parameters, raw string) error {
			value, err := ParseVectorValue(raw)
			if err != nil {
				return err
			}
			*field(parameters) = &value
			return nil
		},
		text: func(parameters Parameters) string {
			value := *field(&parameters)
			if value == nil {
				return ""
			}
			return value.Text()
		},
		shape: func(parameters Parameters) (int, int) {
			value := *field(&parameters)
			if value == nil {
				return 0, 0
			}
			return 1, value.Len()
		},
	}
}

func channelNamesField(
	name, label string,
	field func(*Parameters) **ChannelNames,
) parameterDefinition {
	return parameterDefinition{
		Name: name, Label: label, Type: "text",
		Placeholder: "feed, recycle",
		Help:        "Names must be nonempty and unique.",
		set: func(parameters *Parameters, raw string) error {
			value, err := ParseChannelNames(raw)
			if err != nil {
				return err
			}
			*field(parameters) = &value
			return nil
		},
		text: func(parameters Parameters) string {
			value := *field(&parameters)
			if value == nil {
				return ""
			}
			return value.Text()
		},
		shape: func(parameters Parameters) (int, int) {
			value := *field(&parameters)
			if value == nil {
				return 0, 0
			}
			return 1, value.Len()
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
	parameters.A = cloneMatrixValue(parameters.A)
	parameters.B = cloneMatrixValue(parameters.B)
	parameters.C = cloneMatrixValue(parameters.C)
	parameters.D = cloneMatrixValue(parameters.D)
	parameters.TransferDelays = cloneMatrixValue(parameters.TransferDelays)
	parameters.InputNames = cloneChannelNames(parameters.InputNames)
	parameters.OutputNames = cloneChannelNames(parameters.OutputNames)
	parameters.StateNames = cloneChannelNames(parameters.StateNames)
	parameters.Vector = cloneVectorValue(parameters.Vector)
	parameters.TransferNumerators = clonePolynomialMatrixValue(parameters.TransferNumerators)
	parameters.TransferDenominators = clonePolynomialMatrixValue(parameters.TransferDenominators)
	parameters.Zeros = cloneComplexRootMatrixValue(parameters.Zeros)
	parameters.Poles = cloneComplexRootMatrixValue(parameters.Poles)
	parameters.Frequencies = cloneVectorValue(parameters.Frequencies)
	parameters.FrequencyResponse = cloneComplexResponseValue(parameters.FrequencyResponse)
	return parameters
}

func (b Block) EditorFields() []ParameterField {
	definition := blockDefinitions[b.Kind]
	fields := make([]ParameterField, 0, len(definition.Parameters))
	for _, field := range definition.Parameters {
		options := make([]ParameterOption, len(field.Options))
		value := field.text(b.Parameters)
		for i, option := range field.Options {
			options[i] = ParameterOption{
				Value: option.Value, Label: option.Label, Selected: option.Value == value,
			}
		}
		rows, columns := 0, 0
		if field.shape != nil {
			rows, columns = field.shape(b.Parameters)
		}
		fields = append(fields, ParameterField{
			Name: field.Name, Label: field.Label, Type: field.Type,
			Value: value, Options: options, Rows: rows, Columns: columns,
			Multiline: field.Type == "textarea",
			Step:      field.Step, Min: field.Min, Max: field.Max, Unit: field.Unit,
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
// (validateBlockUpdate) and the compile path (simulate.go's compileModel) call
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
	value, err := ParseVectorValue(raw)
	if err != nil {
		return nil, err
	}
	return value.Values(), nil
}

func coefficientsText(coefficients []float64) string {
	value, err := NewVectorValue(coefficients)
	if err != nil {
		return ""
	}
	return value.Text()
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
