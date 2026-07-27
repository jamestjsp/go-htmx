package studio

import (
	"errors"
	"fmt"
	"strings"
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
	BlockSource BlockKind = "source"
	BlockGain   BlockKind = "gain"
	BlockLag    BlockKind = "lag"
	BlockSum    BlockKind = "sum"
	BlockScope  BlockKind = "scope"
)

func (k BlockKind) Valid() bool {
	switch k {
	case BlockSource, BlockGain, BlockLag, BlockSum, BlockScope:
		return true
	default:
		return false
	}
}

func (k BlockKind) Label() string {
	switch k {
	case BlockSource:
		return "Source"
	case BlockGain:
		return "Gain"
	case BlockLag:
		return "First-order lag"
	case BlockSum:
		return "Sum"
	case BlockScope:
		return "Scope"
	default:
		return "Unknown"
	}
}

func (k BlockKind) HasInput() bool {
	return k != BlockSource
}

func (k BlockKind) HasOutput() bool {
	return k != BlockScope
}

type Parameters struct {
	Amplitude    float64 `json:"amplitude,omitempty"`
	Gain         float64 `json:"gain,omitempty"`
	TimeConstant float64 `json:"timeConstant,omitempty"`
}

func defaultParameters(kind BlockKind) Parameters {
	switch kind {
	case BlockSource:
		return Parameters{Amplitude: 1}
	case BlockGain:
		return Parameters{Gain: 1}
	case BlockLag:
		return Parameters{TimeConstant: 1}
	default:
		return Parameters{}
	}
}

type Point struct {
	X int
	Y int
}

type Flow struct {
	ID             int64
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

func (b Block) ParameterLabel() string {
	switch b.Kind {
	case BlockSource:
		return "Step amplitude"
	case BlockGain:
		return "Gain"
	case BlockLag:
		return "Time constant"
	default:
		return ""
	}
}

func (b Block) ParameterValue() float64 {
	switch b.Kind {
	case BlockSource:
		return b.Parameters.Amplitude
	case BlockGain:
		return b.Parameters.Gain
	case BlockLag:
		return b.Parameters.TimeConstant
	default:
		return 0
	}
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

type Simulation struct {
	ID         int64     `json:"id"`
	CreatedAt  time.Time `json:"createdAt"`
	Duration   float64   `json:"duration"`
	SampleTime float64   `json:"sampleTime"`
	Times      []float64 `json:"times"`
	Series     []Series  `json:"series"`
	Metrics    []Metric  `json:"metrics"`
}

type Snapshot struct {
	Flow        Flow
	Blocks      []Block
	Connections []Connection
	Events      []Event
	LastRun     *Simulation
}

type BlockUpdate struct {
	Name      string
	Parameter float64
}

func validateBlockUpdate(block Block, update BlockUpdate) (Block, error) {
	name := strings.TrimSpace(update.Name)
	if name == "" {
		return Block{}, invalid("block name is required")
	}
	if len(name) > 48 {
		return Block{}, invalid("block name must be 48 characters or fewer")
	}

	block.Name = name
	switch block.Kind {
	case BlockSource:
		if update.Parameter < -100 || update.Parameter > 100 {
			return Block{}, invalid("source amplitude must be between -100 and 100")
		}
		block.Parameters.Amplitude = update.Parameter
	case BlockGain:
		if update.Parameter < -100 || update.Parameter > 100 {
			return Block{}, invalid("gain must be between -100 and 100")
		}
		block.Parameters.Gain = update.Parameter
	case BlockLag:
		if update.Parameter < 0.05 || update.Parameter > 100 {
			return Block{}, invalid("time constant must be between 0.05 and 100 seconds")
		}
		block.Parameters.TimeConstant = update.Parameter
	}
	return block, nil
}

func clampPosition(point Point) Point {
	point.X = max(20, min(point.X, 1040))
	point.Y = max(20, min(point.Y, 500))
	return point
}
