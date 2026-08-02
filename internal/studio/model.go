package studio

import (
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("studio: not found")

type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func invalid(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

type BlockKind string

const (
	BlockSource              BlockKind = "source"
	BlockConstant            BlockKind = "constant"
	BlockVectorConstant      BlockKind = "vector_constant"
	BlockSine                BlockKind = "sine"
	BlockGain                BlockKind = "gain"
	BlockMatrixGain          BlockKind = "matrix_gain"
	BlockMux                 BlockKind = "mux"
	BlockDemux               BlockKind = "demux"
	BlockSelector            BlockKind = "selector"
	BlockPermutation         BlockKind = "permutation"
	BlockSum                 BlockKind = "sum"
	BlockVectorSum           BlockKind = "vector_sum"
	BlockLag                 BlockKind = "lag"
	BlockIntegrator          BlockKind = "integrator"
	BlockTransfer            BlockKind = "transfer"
	BlockPID                 BlockKind = "pid"
	BlockPID2                BlockKind = "pid2"
	BlockDelay               BlockKind = "delay"
	BlockStateSpace          BlockKind = "state_space"
	BlockMIMOTransfer        BlockKind = "mimo_transfer"
	BlockZPK                 BlockKind = "zpk"
	BlockFRD                 BlockKind = "frd"
	BlockUnitDelay           BlockKind = "unit_delay"
	BlockDiscreteTransfer    BlockKind = "discrete_transfer"
	BlockDiscreteStateSpace  BlockKind = "discrete_state_space"
	BlockDiscretizedTransfer BlockKind = "discretized_transfer"
	BlockScope               BlockKind = "scope"
	BlockSpectrum            BlockKind = "spectrum"
	BlockVectorScope         BlockKind = "vector_scope"
)

func (k BlockKind) Valid() bool {
	_, ok := blockDefinitions[k]
	return ok
}

func (k BlockKind) Label() string {
	if definition, ok := blockDefinitions[k]; ok {
		return definition.Label
	}
	return "Unknown"
}

func (k BlockKind) HasInput() bool {
	return k.arity() != arityNone
}

func (k BlockKind) HasOutput() bool {
	return blockDefinitions[k].role != roleSink
}

type Parameters struct {
	Amplitude            float64                 `json:"amplitude,omitempty"`
	InitialValue         float64                 `json:"initialValue,omitempty"`
	InitialCondition     float64                 `json:"initialCondition,omitempty"`
	InitialOutput        float64                 `json:"initialOutput,omitempty"`
	StepTime             float64                 `json:"stepTime,omitempty"`
	Value                float64                 `json:"value,omitempty"`
	Bias                 float64                 `json:"bias,omitempty"`
	Frequency            float64                 `json:"frequency,omitempty"`
	Phase                float64                 `json:"phase,omitempty"`
	Gain                 float64                 `json:"gain,omitempty"`
	Signs                string                  `json:"signs,omitempty"`
	TimeConstant         float64                 `json:"timeConstant,omitempty"`
	Numerator            []float64               `json:"numerator,omitempty"`
	Denominator          []float64               `json:"denominator,omitempty"`
	Proportional         float64                 `json:"proportional,omitempty"`
	Integral             float64                 `json:"integral,omitempty"`
	Derivative           float64                 `json:"derivative,omitempty"`
	FilterTime           float64                 `json:"filterTime,omitempty"`
	SetpointWeight       float64                 `json:"setpointWeight,omitempty"`
	DerivativeWeight     float64                 `json:"derivativeWeight,omitempty"`
	Delay                float64                 `json:"delay,omitempty"`
	DelayMode            string                  `json:"delayMode,omitempty"`
	Approximation        int                     `json:"approximation,omitempty"`
	SampleTime           float64                 `json:"sampleTime,omitempty"`
	SampleTimeMode       string                  `json:"sampleTimeMode,omitempty"`
	ConversionMethod     string                  `json:"conversionMethod,omitempty"`
	TimeDomain           string                  `json:"timeDomain,omitempty"`
	FrequencyUnit        string                  `json:"frequencyUnit,omitempty"`
	ResponseUnit         string                  `json:"responseUnit,omitempty"`
	A                    *MatrixValue            `json:"a,omitempty"`
	B                    *MatrixValue            `json:"b,omitempty"`
	C                    *MatrixValue            `json:"c,omitempty"`
	D                    *MatrixValue            `json:"d,omitempty"`
	InputNames           *ChannelNames           `json:"inputNames,omitempty"`
	OutputNames          *ChannelNames           `json:"outputNames,omitempty"`
	StateNames           *ChannelNames           `json:"stateNames,omitempty"`
	InitialState         *VectorValue            `json:"initialState,omitempty"`
	Vector               *VectorValue            `json:"vector,omitempty"`
	TransferNumerators   *PolynomialMatrixValue  `json:"transferNumerators,omitempty"`
	TransferDenominators *PolynomialMatrixValue  `json:"transferDenominators,omitempty"`
	TransferDelays       *MatrixValue            `json:"transferDelays,omitempty"`
	Zeros                *ComplexRootMatrixValue `json:"zeros,omitempty"`
	Poles                *ComplexRootMatrixValue `json:"poles,omitempty"`
	Frequencies          *VectorValue            `json:"frequencies,omitempty"`
	FrequencyResponse    *ComplexResponseValue   `json:"frequencyResponse,omitempty"`
}

// Sheet geometry. The flowsheet is a fixed world measured in sheet
// coordinates; the client pans and zooms a viewport across it. Blocks always
// sit on the grid, so a replayed or hand-edited request cannot place one
// between intersections.
const (
	GridPitch   = 20
	BlockWidth  = 172
	BlockHeight = 84
	SheetWidth  = 6000
	SheetHeight = 4000
)

// maxBlocksPerRequest bounds the batch operations so one request cannot ask
// for unbounded work.
const maxBlocksPerRequest = 256

type Point struct {
	X int
	Y int
}

type Project struct {
	ID        int64
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Flow struct {
	ID        int64
	ProjectID int64
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	// ModelUpdatedAt moves whenever the flowsheet's model changes, so a
	// simulation older than it is stale.
	ModelUpdatedAt time.Time
	// NeedsRun is that staleness as the interface states it: no simulation
	// run exists at or after ModelUpdatedAt. The tab dot and the simulation
	// dock read the same flag so they cannot disagree.
	NeedsRun bool
}

type Block struct {
	ID         int64
	FlowID     int64
	Kind       BlockKind
	Name       string
	Position   Point
	Parameters Parameters
}

// Connection is one drawn signal. SourcePort and TargetPort are the terminal
// indices it joins, numbered from zero: which output of the source drives
// which input of the target. Every block but Sum has a single terminal in
// each direction, so port 0 is what almost every wire names.
type Connection struct {
	ID         int64
	FlowID     int64
	SourceID   int64
	SourcePort int
	TargetID   int64
	TargetPort int
}

// Wire is a connection a caller is asking for, before it exists. Ports are
// numbered from zero, so the zero value names each block's first terminal —
// which is exactly what a client that predates ports asks for when it omits
// them.
type Wire struct {
	SourceID   int64
	SourcePort int
	TargetID   int64
	TargetPort int
}

type Event struct {
	ID        int64
	Message   string
	CreatedAt time.Time
}

type SimulationRequest struct {
	Duration   float64
	SampleTime float64
}

type ResultChannel struct {
	BlockID     int64  `json:"blockId"`
	Port        int    `json:"port"`
	Channel     int    `json:"channel"`
	ChannelName string `json:"channelName,omitempty"`
	Name        string `json:"name"`
}

func (channel ResultChannel) Ref() ChannelRef {
	return ChannelRef{
		BlockID: channel.BlockID, Port: channel.Port, Channel: channel.Channel,
	}
}

type Series struct {
	ResultChannel
	Values []float64 `json:"values"`
}

type Metric struct {
	ResultChannel
	Peak       float64 `json:"peak"`
	Final      float64 `json:"final"`
	Settled    bool    `json:"settled"`
	SettleTime float64 `json:"settleTime"`
}

type Spectrum struct {
	ResultChannel
	Frequencies   []float64 `json:"frequencies"`
	Magnitudes    []float64 `json:"magnitudes"`
	PeakFrequency float64   `json:"peakFrequency"`
	PeakMagnitude float64   `json:"peakMagnitude"`
}

type Simulation struct {
	ID         int64      `json:"id"`
	CreatedAt  time.Time  `json:"createdAt"`
	Duration   float64    `json:"duration"`
	SampleTime float64    `json:"sampleTime"`
	Fidelity   Fidelity   `json:"fidelity,omitempty"`
	Times      []float64  `json:"times"`
	Series     []Series   `json:"series"`
	Metrics    []Metric   `json:"metrics"`
	Spectra    []Spectrum `json:"spectra,omitempty"`
}

type Fidelity struct {
	BaseStep          float64           `json:"baseStep"`
	ModelDomain       string            `json:"modelDomain"`
	Driver            string            `json:"driver"`
	SourceHold        string            `json:"sourceHold"`
	SegmentCount      int               `json:"segmentCount"`
	BlockRates        []BlockRate       `json:"blockRates,omitempty"`
	RateTransitions   []RateTransition  `json:"rateTransitions,omitempty"`
	Delays            []DelayProvenance `json:"delays,omitempty"`
	DelayModels       []string          `json:"delayModels,omitempty"`
	ExactDelayAligned bool              `json:"exactDelayAligned,omitempty"`
}

type BlockRate struct {
	BlockID     int64   `json:"blockId"`
	BlockName   string  `json:"blockName"`
	Mode        string  `json:"mode"`
	SampleTime  float64 `json:"sampleTime"`
	UpdateEvery int     `json:"updateEvery"`
}

type RateTransition struct {
	SourceBlockID int64   `json:"sourceBlockId"`
	TargetBlockID int64   `json:"targetBlockId"`
	SourceRate    float64 `json:"sourceRate"`
	TargetRate    float64 `json:"targetRate"`
	Hold          string  `json:"hold"`
}

type DelayProvenance struct {
	BlockID            int64   `json:"blockId"`
	BlockName          string  `json:"blockName"`
	Representation     string  `json:"representation"`
	Delay              float64 `json:"delay"`
	ApproximationOrder int     `json:"approximationOrder,omitempty"`
	SampleTime         float64 `json:"sampleTime,omitempty"`
	SampleTimeMode     string  `json:"sampleTimeMode,omitempty"`
	Aligned            bool    `json:"aligned,omitempty"`
}

type Snapshot struct {
	Flow        Flow
	Blocks      []Block
	Connections []Connection
	Events      []Event
	LastRun     *Simulation
}

type Workspace struct {
	Projects []Project
	Project  Project
	Flows    []Flow
	Snapshot Snapshot
	Analysis AnalysisWorkspace
}

type BlockUpdate struct {
	Name       string
	Parameters map[string]string
}

// clampPosition keeps a block wholly inside the sheet and on the grid.
func clampPosition(point Point) Point {
	point.X = snapWithin(point.X, SheetWidth-BlockWidth)
	point.Y = snapWithin(point.Y, SheetHeight-BlockHeight)
	return point
}

// snapWithin clamps value to 0..limit and rounds it to the nearest grid
// intersection that still fits.
func snapWithin(value, limit int) int {
	value = max(0, min(value, limit))
	snapped := (value + GridPitch/2) / GridPitch * GridPitch
	if snapped > limit {
		snapped -= GridPitch
	}
	return snapped
}
