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
	BlockSource     BlockKind = "source"
	BlockConstant   BlockKind = "constant"
	BlockSine       BlockKind = "sine"
	BlockGain       BlockKind = "gain"
	BlockSum        BlockKind = "sum"
	BlockLag        BlockKind = "lag"
	BlockIntegrator BlockKind = "integrator"
	BlockTransfer   BlockKind = "transfer"
	BlockPID        BlockKind = "pid"
	BlockDelay      BlockKind = "delay"
	BlockScope      BlockKind = "scope"
	BlockSpectrum   BlockKind = "spectrum"
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
	return blockDefinitions[k].HasInput
}

func (k BlockKind) HasOutput() bool {
	return blockDefinitions[k].HasOutput
}

type Parameters struct {
	Amplitude     float64   `json:"amplitude,omitempty"`
	InitialValue  float64   `json:"initialValue,omitempty"`
	StepTime      float64   `json:"stepTime,omitempty"`
	Value         float64   `json:"value,omitempty"`
	Bias          float64   `json:"bias,omitempty"`
	Frequency     float64   `json:"frequency,omitempty"`
	Phase         float64   `json:"phase,omitempty"`
	Gain          float64   `json:"gain,omitempty"`
	Signs         string    `json:"signs,omitempty"`
	TimeConstant  float64   `json:"timeConstant,omitempty"`
	Numerator     []float64 `json:"numerator,omitempty"`
	Denominator   []float64 `json:"denominator,omitempty"`
	Proportional  float64   `json:"proportional,omitempty"`
	Integral      float64   `json:"integral,omitempty"`
	Derivative    float64   `json:"derivative,omitempty"`
	FilterTime    float64   `json:"filterTime,omitempty"`
	Delay         float64   `json:"delay,omitempty"`
	Approximation int       `json:"approximation,omitempty"`
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
	ID             int64
	ProjectID      int64
	Name           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ModelUpdatedAt time.Time
}

type Block struct {
	ID         int64
	FlowID     int64
	Kind       BlockKind
	Name       string
	Position   Point
	Parameters Parameters
}

type Connection struct {
	ID       int64
	FlowID   int64
	SourceID int64
	TargetID int64
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

type Series struct {
	BlockID int64     `json:"blockId"`
	Name    string    `json:"name"`
	Values  []float64 `json:"values"`
}

type Metric struct {
	Name       string  `json:"name"`
	Peak       float64 `json:"peak"`
	Final      float64 `json:"final"`
	Settled    bool    `json:"settled"`
	SettleTime float64 `json:"settleTime"`
}

type Spectrum struct {
	BlockID       int64     `json:"blockId"`
	Name          string    `json:"name"`
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
	Times      []float64  `json:"times"`
	Series     []Series   `json:"series"`
	Metrics    []Metric   `json:"metrics"`
	Spectra    []Spectrum `json:"spectra,omitempty"`
}

type Snapshot struct {
	Flow        Flow
	Blocks      []Block
	Connections []Connection
	Events      []Event
	LastRun     *Simulation
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
