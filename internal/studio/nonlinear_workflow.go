package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

const (
	nonlinearDefinitionSchemaVersion = 1
	defaultEquilibriumTolerance      = 1e-8
	maxNonlinearValidityDirections   = 32
	maxEKFBatchSteps                 = 10000
)

var nonlinearDefinitionKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)

type NonlinearDefinitionRef struct {
	Key     string `json:"key"`
	Version int    `json:"version"`
}

type NonlinearDefinition struct {
	Ref         NonlinearDefinitionRef `json:"ref"`
	Name        string                 `json:"name"`
	StateNames  []string               `json:"stateNames"`
	InputNames  []string               `json:"inputNames"`
	OutputNames []string               `json:"outputNames"`
}

type NonlinearRuntimeCallbacks struct {
	Dynamics            func(x, u *mat.VecDense) *mat.VecDense
	Output              func(x, u *mat.VecDense) *mat.VecDense
	Transition          func(x, u *mat.VecDense) *mat.VecDense
	Measurement         func(x *mat.VecDense) *mat.VecDense
	TransitionJacobian  func(x, u *mat.VecDense) *mat.Dense
	MeasurementJacobian func(x *mat.VecDense) *mat.Dense
}

type nonlinearRuntimeEntry struct {
	definition   NonlinearDefinition
	callbacks    NonlinearRuntimeCallbacks
	registeredAt time.Time
}

var nonlinearRuntimeDefinitions = struct {
	sync.RWMutex
	entries map[NonlinearDefinitionRef]nonlinearRuntimeEntry
}{
	entries: make(map[NonlinearDefinitionRef]nonlinearRuntimeEntry),
}

type NamedOperatingPoint struct {
	Name                 string                 `json:"name"`
	Definition           NonlinearDefinitionRef `json:"definition"`
	State                []float64              `json:"state"`
	Input                []float64              `json:"input"`
	EquilibriumTolerance float64                `json:"equilibriumTolerance,omitempty"`
}

type NonlinearValidityDirection struct {
	Name       string    `json:"name"`
	StateDelta []float64 `json:"stateDelta"`
	InputDelta []float64 `json:"inputDelta"`
	Radius     float64   `json:"radius"`
}

type DirectionalValidityEvidence struct {
	Name                         string    `json:"name"`
	StateDirection               []float64 `json:"stateDirection"`
	InputDirection               []float64 `json:"inputDirection"`
	Radius                       float64   `json:"radius"`
	StateErrorNorm               float64   `json:"stateErrorNorm"`
	OutputErrorNorm              float64   `json:"outputErrorNorm"`
	HalfRadiusStateErrorNorm     float64   `json:"halfRadiusStateErrorNorm"`
	HalfRadiusOutputErrorNorm    float64   `json:"halfRadiusOutputErrorNorm"`
	StateErrorOverRadiusSquared  float64   `json:"stateErrorOverRadiusSquared"`
	OutputErrorOverRadiusSquared float64   `json:"outputErrorOverRadiusSquared"`
	StateQuadraticRatio          *float64  `json:"stateQuadraticRatio,omitempty"`
	OutputQuadraticRatio         *float64  `json:"outputQuadraticRatio,omitempty"`
}

type NonlinearModelProvenance struct {
	Definition          NonlinearDefinitionRef `json:"definition"`
	DefinitionName      string                 `json:"definitionName"`
	OperatingPointName  string                 `json:"operatingPointName"`
	RuntimeRegisteredAt time.Time              `json:"runtimeRegisteredAt"`
	CreatedAt           time.Time              `json:"createdAt"`
	Method              string                 `json:"method"`
}

type NonlinearLinearizationRequest struct {
	OperatingPoint NamedOperatingPoint          `json:"operatingPoint"`
	Directions     []NonlinearValidityDirection `json:"directions"`
}

type NonlinearLinearizationCandidate struct {
	Definition          NonlinearDefinition           `json:"definition"`
	OperatingPoint      NamedOperatingPoint           `json:"operatingPoint"`
	EquilibriumResidual []float64                     `json:"equilibriumResidual"`
	EquilibriumNorm     float64                       `json:"equilibriumNorm"`
	OperatingOutput     []float64                     `json:"operatingOutput"`
	Validity            []DirectionalValidityEvidence `json:"validity"`
	Provenance          NonlinearModelProvenance      `json:"provenance"`
	CandidateOnly       bool                          `json:"candidateOnly"`
	System              *controlsys.System            `json:"-"`
}

type NonlinearEKFDefinition struct {
	Name              string                 `json:"name"`
	Model             NonlinearDefinitionRef `json:"model"`
	InitialState      []float64              `json:"initialState"`
	ProcessNoise      MatrixValue            `json:"processNoise"`
	MeasurementNoise  MatrixValue            `json:"measurementNoise"`
	InitialCovariance MatrixValue            `json:"initialCovariance"`
}

type NonlinearEKFRunRequest struct {
	Estimator    NonlinearEKFDefinition `json:"estimator"`
	Inputs       [][]float64            `json:"inputs"`
	Measurements [][]float64            `json:"measurements"`
}

type NonlinearEKFStep struct {
	Index               int         `json:"index"`
	Input               []float64   `json:"input"`
	Measurement         []float64   `json:"measurement"`
	PredictedState      []float64   `json:"predictedState"`
	PredictedCovariance MatrixValue `json:"predictedCovariance"`
	UpdatedState        []float64   `json:"updatedState"`
	UpdatedCovariance   MatrixValue `json:"updatedCovariance"`
}

type NonlinearEKFRun struct {
	Definition       NonlinearDefinition      `json:"definition"`
	EstimatorName    string                   `json:"estimatorName"`
	StateNames       []string                 `json:"stateNames"`
	InputNames       []string                 `json:"inputNames"`
	MeasurementNames []string                 `json:"measurementNames"`
	Steps            []NonlinearEKFStep       `json:"steps"`
	FinalState       []float64                `json:"finalState"`
	FinalCovariance  MatrixValue              `json:"finalCovariance"`
	Provenance       NonlinearModelProvenance `json:"provenance"`
}

func (s *Studio) RegisterNonlinearDefinition(
	ctx context.Context,
	definition NonlinearDefinition,
	callbacks NonlinearRuntimeCallbacks,
) (NonlinearDefinition, error) {
	definition, err := normalizeNonlinearDefinition(definition)
	if err != nil {
		return NonlinearDefinition{}, err
	}
	if err := validateNonlinearCallbacks(callbacks); err != nil {
		return NonlinearDefinition{}, err
	}
	nonlinearRuntimeDefinitions.Lock()
	defer nonlinearRuntimeDefinitions.Unlock()
	if existing, ok := nonlinearRuntimeDefinitions.entries[definition.Ref]; ok &&
		!reflect.DeepEqual(existing.definition, definition) {
		return NonlinearDefinition{}, invalid(
			"nonlinear definition %s@%d is already registered with different metadata; increment its version",
			definition.Ref.Key, definition.Ref.Version,
		)
	}
	if err := s.persistNonlinearDefinition(ctx, definition); err != nil {
		return NonlinearDefinition{}, err
	}
	entry := nonlinearRuntimeEntry{
		definition: cloneNonlinearDefinition(definition),
		callbacks:  callbacks, registeredAt: s.now().UTC(),
	}
	nonlinearRuntimeDefinitions.entries[definition.Ref] = entry
	return cloneNonlinearDefinition(definition), nil
}

func (s *Studio) NonlinearDefinition(
	ctx context.Context,
	ref NonlinearDefinitionRef,
) (NonlinearDefinition, error) {
	if err := ensureNonlinearDefinitionSchema(ctx, s.db); err != nil {
		return NonlinearDefinition{}, err
	}
	var schemaVersion int
	var encoded string
	err := s.db.QueryRowContext(ctx, `
		SELECT schema_version, definition_json
		FROM nonlinear_definitions
		WHERE definition_key = ? AND definition_version = ?`,
		ref.Key, ref.Version,
	).Scan(&schemaVersion, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return NonlinearDefinition{}, ErrNotFound
	}
	if err != nil {
		return NonlinearDefinition{}, fmt.Errorf("load nonlinear definition: %w", err)
	}
	if schemaVersion != nonlinearDefinitionSchemaVersion {
		return NonlinearDefinition{}, invalid(
			"nonlinear definition storage version %d is unsupported", schemaVersion,
		)
	}
	var definition NonlinearDefinition
	if err := json.Unmarshal([]byte(encoded), &definition); err != nil {
		return NonlinearDefinition{}, fmt.Errorf("decode nonlinear definition: %w", err)
	}
	if definition.Ref != ref {
		return NonlinearDefinition{}, invalid(
			"nonlinear definition storage key/version does not match its payload",
		)
	}
	return cloneNonlinearDefinition(definition), nil
}

func (s *Studio) LinearizeNonlinear(
	ctx context.Context,
	request NonlinearLinearizationRequest,
) (NonlinearLinearizationCandidate, error) {
	point := request.OperatingPoint
	point.Name = strings.TrimSpace(point.Name)
	if point.Name == "" {
		return NonlinearLinearizationCandidate{}, invalid("operating point must have a name")
	}
	definition, entry, err := s.nonlinearRuntimeDefinition(ctx, point.Definition)
	if err != nil {
		return NonlinearLinearizationCandidate{}, err
	}
	if err := validateNamedOperatingPoint(point, definition); err != nil {
		return NonlinearLinearizationCandidate{}, err
	}
	tolerance := point.EquilibriumTolerance
	if tolerance == 0 {
		tolerance = defaultEquilibriumTolerance
	}
	point.EquilibriumTolerance = tolerance
	x0 := mat.NewVecDense(len(point.State), append([]float64(nil), point.State...))
	u0 := mat.NewVecDense(len(point.Input), append([]float64(nil), point.Input...))
	residual, err := checkedVector(
		"dynamics at operating point",
		entry.callbacks.Dynamics(x0, u0),
		len(definition.StateNames),
	)
	if err != nil {
		return NonlinearLinearizationCandidate{}, err
	}
	equilibriumNorm := vectorNorm(residual)
	if equilibriumNorm > tolerance {
		return NonlinearLinearizationCandidate{}, invalid(
			"operating point %q is not an equilibrium: residual norm %.6g exceeds tolerance %.6g",
			point.Name, equilibriumNorm, tolerance,
		)
	}
	output, err := checkedVector(
		"output at operating point",
		entry.callbacks.Output(x0, u0),
		len(definition.OutputNames),
	)
	if err != nil {
		return NonlinearLinearizationCandidate{}, err
	}
	system, err := controlsys.Linearize(
		&controlsys.NonlinearModel{
			F: entry.callbacks.Dynamics,
			H: entry.callbacks.Output,
			N: len(definition.StateNames),
			M: len(definition.InputNames),
			P: len(definition.OutputNames),
		},
		x0,
		u0,
	)
	if err != nil {
		return NonlinearLinearizationCandidate{}, fmt.Errorf(
			"linearize nonlinear definition %s@%d: %w",
			definition.Ref.Key, definition.Ref.Version, err,
		)
	}
	if err := system.SetStateName(definition.StateNames...); err != nil {
		return NonlinearLinearizationCandidate{}, err
	}
	if err := system.SetInputName(definition.InputNames...); err != nil {
		return NonlinearLinearizationCandidate{}, err
	}
	if err := system.SetOutputName(definition.OutputNames...); err != nil {
		return NonlinearLinearizationCandidate{}, err
	}
	validity, err := nonlinearDirectionalValidity(
		entry.callbacks, system, definition, point, request.Directions,
	)
	if err != nil {
		return NonlinearLinearizationCandidate{}, err
	}
	return NonlinearLinearizationCandidate{
		Definition:          cloneNonlinearDefinition(definition),
		OperatingPoint:      cloneNamedOperatingPoint(point),
		EquilibriumResidual: vectorValues(residual),
		EquilibriumNorm:     equilibriumNorm,
		OperatingOutput:     vectorValues(output),
		Validity:            validity,
		Provenance: NonlinearModelProvenance{
			Definition: definition.Ref, DefinitionName: definition.Name,
			OperatingPointName:  point.Name,
			RuntimeRegisteredAt: entry.registeredAt,
			CreatedAt:           s.now().UTC(),
			Method:              "controlsys.Linearize central finite-difference local model",
		},
		CandidateOnly: true,
		System:        system.Copy(),
	}, nil
}

func (s *Studio) RunNonlinearEKF(
	ctx context.Context,
	request NonlinearEKFRunRequest,
) (NonlinearEKFRun, error) {
	estimator := request.Estimator
	estimator.Name = strings.TrimSpace(estimator.Name)
	if estimator.Name == "" {
		return NonlinearEKFRun{}, invalid("EKF definition must have a name")
	}
	definition, entry, err := s.nonlinearRuntimeDefinition(ctx, estimator.Model)
	if err != nil {
		return NonlinearEKFRun{}, err
	}
	n := len(definition.StateNames)
	m := len(definition.InputNames)
	p := len(definition.OutputNames)
	if err := validateFiniteVector("EKF initial state", estimator.InitialState, n); err != nil {
		return NonlinearEKFRun{}, err
	}
	q := denseMatrix(&estimator.ProcessNoise)
	r := denseMatrix(&estimator.MeasurementNoise)
	p0 := denseMatrix(&estimator.InitialCovariance)
	if err := validatePSDCovariance("EKF process noise Q", q, n); err != nil {
		return NonlinearEKFRun{}, err
	}
	if err := validatePSDCovariance("EKF measurement noise R", r, p); err != nil {
		return NonlinearEKFRun{}, err
	}
	if err := validatePSDCovariance("EKF initial covariance P0", p0, n); err != nil {
		return NonlinearEKFRun{}, err
	}
	if len(request.Inputs) == 0 {
		return NonlinearEKFRun{}, invalid("EKF batch requires at least one predict/update step")
	}
	if len(request.Inputs) != len(request.Measurements) {
		return NonlinearEKFRun{}, invalid(
			"EKF batch has %d inputs but %d measurements",
			len(request.Inputs), len(request.Measurements),
		)
	}
	if len(request.Inputs) > maxEKFBatchSteps {
		return NonlinearEKFRun{}, invalid(
			"EKF batch must contain at most %d steps", maxEKFBatchSteps,
		)
	}
	for i := range request.Inputs {
		if err := validateFiniteVector(
			fmt.Sprintf("EKF input %d", i+1), request.Inputs[i], m,
		); err != nil {
			return NonlinearEKFRun{}, err
		}
		if err := validateFiniteVector(
			fmt.Sprintf("EKF measurement %d", i+1), request.Measurements[i], p,
		); err != nil {
			return NonlinearEKFRun{}, err
		}
	}
	filter, err := controlsys.NewEKF(
		&controlsys.EKFModel{
			F:    entry.callbacks.Transition,
			H:    entry.callbacks.Measurement,
			FJac: entry.callbacks.TransitionJacobian,
			HJac: entry.callbacks.MeasurementJacobian,
			Q:    q,
			R:    r,
		},
		mat.NewVecDense(n, append([]float64(nil), estimator.InitialState...)),
		p0,
	)
	if err != nil {
		return NonlinearEKFRun{}, fmt.Errorf("create nonlinear EKF: %w", err)
	}
	result := NonlinearEKFRun{
		Definition:       cloneNonlinearDefinition(definition),
		EstimatorName:    estimator.Name,
		StateNames:       append([]string(nil), definition.StateNames...),
		InputNames:       append([]string(nil), definition.InputNames...),
		MeasurementNames: append([]string(nil), definition.OutputNames...),
		Provenance: NonlinearModelProvenance{
			Definition: definition.Ref, DefinitionName: definition.Name,
			RuntimeRegisteredAt: entry.registeredAt,
			CreatedAt:           s.now().UTC(),
			Method:              "controlsys.EKF full-batch predict/update",
		},
	}
	for i := range request.Inputs {
		input := mat.NewVecDense(m, append([]float64(nil), request.Inputs[i]...))
		if err := filter.Predict(input); err != nil {
			return NonlinearEKFRun{}, fmt.Errorf("EKF predict step %d: %w", i+1, err)
		}
		predictedCovariance, err := matrixValueFromDense(filter.P)
		if err != nil {
			return NonlinearEKFRun{}, err
		}
		step := NonlinearEKFStep{
			Index:               i,
			Input:               append([]float64(nil), request.Inputs[i]...),
			Measurement:         append([]float64(nil), request.Measurements[i]...),
			PredictedState:      vectorValues(filter.X),
			PredictedCovariance: predictedCovariance,
		}
		measurement := mat.NewVecDense(
			p, append([]float64(nil), request.Measurements[i]...,
			))
		if err := filter.Update(measurement); err != nil {
			return NonlinearEKFRun{}, fmt.Errorf("EKF update step %d: %w", i+1, err)
		}
		step.UpdatedState = vectorValues(filter.X)
		step.UpdatedCovariance, err = matrixValueFromDense(filter.P)
		if err != nil {
			return NonlinearEKFRun{}, err
		}
		result.Steps = append(result.Steps, step)
	}
	result.FinalState = vectorValues(filter.X)
	result.FinalCovariance, err = matrixValueFromDense(filter.P)
	if err != nil {
		return NonlinearEKFRun{}, err
	}
	return result, nil
}

func normalizeNonlinearDefinition(
	definition NonlinearDefinition,
) (NonlinearDefinition, error) {
	definition.Ref.Key = strings.TrimSpace(definition.Ref.Key)
	definition.Name = strings.TrimSpace(definition.Name)
	if !nonlinearDefinitionKeyPattern.MatchString(definition.Ref.Key) {
		return NonlinearDefinition{}, invalid(
			"nonlinear definition key must be 1-128 stable letters, digits, '.', '_', '/', or '-'",
		)
	}
	if definition.Ref.Version <= 0 {
		return NonlinearDefinition{}, invalid("nonlinear definition version must be positive")
	}
	if definition.Name == "" {
		return NonlinearDefinition{}, invalid("nonlinear definition must have a name")
	}
	var err error
	definition.StateNames, err = normalizedSignalNames(
		"nonlinear state", definition.StateNames, true,
	)
	if err != nil {
		return NonlinearDefinition{}, err
	}
	definition.InputNames, err = normalizedSignalNames(
		"nonlinear input", definition.InputNames, false,
	)
	if err != nil {
		return NonlinearDefinition{}, err
	}
	definition.OutputNames, err = normalizedSignalNames(
		"nonlinear output", definition.OutputNames, true,
	)
	if err != nil {
		return NonlinearDefinition{}, err
	}
	return definition, nil
}

func normalizedSignalNames(
	role string,
	names []string,
	required bool,
) ([]string, error) {
	if required && len(names) == 0 {
		return nil, invalid("assign at least one %s name", role)
	}
	result := make([]string, len(names))
	seen := make(map[string]struct{}, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, invalid("%s name %d is empty", role, i+1)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, invalid("%s name %q is duplicated", role, name)
		}
		seen[name] = struct{}{}
		result[i] = name
	}
	return result, nil
}

func validateNonlinearCallbacks(callbacks NonlinearRuntimeCallbacks) error {
	if callbacks.Dynamics == nil || callbacks.Output == nil ||
		callbacks.Transition == nil || callbacks.Measurement == nil ||
		callbacks.TransitionJacobian == nil ||
		callbacks.MeasurementJacobian == nil {
		return invalid(
			"nonlinear runtime requires dynamics, output, transition, measurement, and both EKF Jacobian callbacks",
		)
	}
	return nil
}

func (s *Studio) persistNonlinearDefinition(
	ctx context.Context,
	definition NonlinearDefinition,
) error {
	encoded, err := json.Marshal(definition)
	if err != nil {
		return fmt.Errorf("encode nonlinear definition: %w", err)
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := ensureNonlinearDefinitionSchema(ctx, tx); err != nil {
			return err
		}
		var existing string
		err := tx.QueryRowContext(ctx, `
			SELECT definition_json
			FROM nonlinear_definitions
			WHERE definition_key = ? AND definition_version = ?`,
			definition.Ref.Key, definition.Ref.Version,
		).Scan(&existing)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.ExecContext(ctx, `
				INSERT INTO nonlinear_definitions(
					definition_key, definition_version, schema_version,
					definition_json, created_at
				) VALUES(?, ?, ?, ?, ?)`,
				definition.Ref.Key, definition.Ref.Version,
				nonlinearDefinitionSchemaVersion, string(encoded),
				s.now().UTC().Format(time.RFC3339Nano),
			)
			if err != nil {
				return fmt.Errorf("store nonlinear definition: %w", err)
			}
			return nil
		case err != nil:
			return fmt.Errorf("load nonlinear definition version: %w", err)
		case existing != string(encoded):
			return invalid(
				"nonlinear definition %s@%d is already persisted with different metadata; increment its version",
				definition.Ref.Key, definition.Ref.Version,
			)
		default:
			return nil
		}
	})
}

type nonlinearSchemaExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func ensureNonlinearDefinitionSchema(
	ctx context.Context,
	executor nonlinearSchemaExecutor,
) error {
	_, err := executor.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS nonlinear_definitions (
			definition_key TEXT NOT NULL,
			definition_version INTEGER NOT NULL,
			schema_version INTEGER NOT NULL,
			definition_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(definition_key, definition_version)
		)`)
	if err != nil {
		return fmt.Errorf("create nonlinear definition storage: %w", err)
	}
	return nil
}

func (s *Studio) nonlinearRuntimeDefinition(
	ctx context.Context,
	ref NonlinearDefinitionRef,
) (NonlinearDefinition, nonlinearRuntimeEntry, error) {
	definition, err := s.NonlinearDefinition(ctx, ref)
	if err != nil {
		return NonlinearDefinition{}, nonlinearRuntimeEntry{}, err
	}
	nonlinearRuntimeDefinitions.RLock()
	entry, ok := nonlinearRuntimeDefinitions.entries[ref]
	nonlinearRuntimeDefinitions.RUnlock()
	if !ok {
		return NonlinearDefinition{}, nonlinearRuntimeEntry{}, invalid(
			"nonlinear definition %s@%d is persisted but its runtime callbacks are not registered",
			ref.Key, ref.Version,
		)
	}
	if !reflect.DeepEqual(entry.definition, definition) {
		return NonlinearDefinition{}, nonlinearRuntimeEntry{}, invalid(
			"nonlinear definition %s@%d metadata does not match its runtime registration",
			ref.Key, ref.Version,
		)
	}
	return definition, entry, nil
}

func validateNamedOperatingPoint(
	point NamedOperatingPoint,
	definition NonlinearDefinition,
) error {
	if point.Definition != definition.Ref {
		return invalid("operating point definition does not match the loaded nonlinear model")
	}
	if err := validateFiniteVector(
		"operating state", point.State, len(definition.StateNames),
	); err != nil {
		return err
	}
	if err := validateFiniteVector(
		"operating input", point.Input, len(definition.InputNames),
	); err != nil {
		return err
	}
	tolerance := point.EquilibriumTolerance
	if tolerance < 0 || math.IsNaN(tolerance) || math.IsInf(tolerance, 0) {
		return invalid("equilibrium tolerance must be a non-negative finite number")
	}
	return nil
}

func nonlinearDirectionalValidity(
	callbacks NonlinearRuntimeCallbacks,
	system *controlsys.System,
	definition NonlinearDefinition,
	point NamedOperatingPoint,
	directions []NonlinearValidityDirection,
) ([]DirectionalValidityEvidence, error) {
	if len(directions) == 0 {
		return nil, invalid("provide at least one local validity direction")
	}
	if len(directions) > maxNonlinearValidityDirections {
		return nil, invalid(
			"provide at most %d local validity directions",
			maxNonlinearValidityDirections,
		)
	}
	x0 := mat.NewVecDense(len(point.State), point.State)
	u0 := mat.NewVecDense(len(point.Input), point.Input)
	f0, err := checkedVector(
		"dynamics at operating point",
		callbacks.Dynamics(x0, u0),
		len(definition.StateNames),
	)
	if err != nil {
		return nil, err
	}
	h0, err := checkedVector(
		"output at operating point",
		callbacks.Output(x0, u0),
		len(definition.OutputNames),
	)
	if err != nil {
		return nil, err
	}
	result := make([]DirectionalValidityEvidence, 0, len(directions))
	for i, direction := range directions {
		direction.Name = strings.TrimSpace(direction.Name)
		if direction.Name == "" {
			return nil, invalid("local validity direction %d must have a name", i+1)
		}
		if err := validateFiniteVector(
			fmt.Sprintf("direction %q state", direction.Name),
			direction.StateDelta, len(definition.StateNames),
		); err != nil {
			return nil, err
		}
		if err := validateFiniteVector(
			fmt.Sprintf("direction %q input", direction.Name),
			direction.InputDelta, len(definition.InputNames),
		); err != nil {
			return nil, err
		}
		if direction.Radius <= 0 || math.IsNaN(direction.Radius) ||
			math.IsInf(direction.Radius, 0) {
			return nil, invalid(
				"direction %q radius must be positive and finite", direction.Name,
			)
		}
		if combinedDirectionNorm(direction.StateDelta, direction.InputDelta) == 0 {
			return nil, invalid("direction %q cannot be all zero", direction.Name)
		}
		stateError, outputError, err := directionalApproximationErrors(
			callbacks, system, x0, u0, f0, h0,
			direction.StateDelta, direction.InputDelta, direction.Radius,
		)
		if err != nil {
			return nil, err
		}
		halfStateError, halfOutputError, err := directionalApproximationErrors(
			callbacks, system, x0, u0, f0, h0,
			direction.StateDelta, direction.InputDelta, direction.Radius/2,
		)
		if err != nil {
			return nil, err
		}
		evidence := DirectionalValidityEvidence{
			Name:                         direction.Name,
			StateDirection:               append([]float64(nil), direction.StateDelta...),
			InputDirection:               append([]float64(nil), direction.InputDelta...),
			Radius:                       direction.Radius,
			StateErrorNorm:               stateError,
			OutputErrorNorm:              outputError,
			HalfRadiusStateErrorNorm:     halfStateError,
			HalfRadiusOutputErrorNorm:    halfOutputError,
			StateErrorOverRadiusSquared:  stateError / (direction.Radius * direction.Radius),
			OutputErrorOverRadiusSquared: outputError / (direction.Radius * direction.Radius),
			StateQuadraticRatio:          finiteErrorRatio(stateError, halfStateError),
			OutputQuadraticRatio:         finiteErrorRatio(outputError, halfOutputError),
		}
		result = append(result, evidence)
	}
	return result, nil
}

func directionalApproximationErrors(
	callbacks NonlinearRuntimeCallbacks,
	system *controlsys.System,
	x0, u0, f0, h0 *mat.VecDense,
	stateDirection, inputDirection []float64,
	radius float64,
) (float64, float64, error) {
	dx := scaledVector(stateDirection, radius)
	du := scaledVector(inputDirection, radius)
	x := mat.VecDenseCopyOf(x0)
	x.AddVec(x, dx)
	u := mat.VecDenseCopyOf(u0)
	u.AddVec(u, du)
	actualState, err := checkedVector(
		"directional dynamics", callbacks.Dynamics(x, u), x0.Len(),
	)
	if err != nil {
		return 0, 0, err
	}
	actualOutput, err := checkedVector(
		"directional output", callbacks.Output(x, u), h0.Len(),
	)
	if err != nil {
		return 0, 0, err
	}
	predictedState := mat.VecDenseCopyOf(f0)
	var aDX, bDU mat.VecDense
	aDX.MulVec(system.A, dx)
	bDU.MulVec(system.B, du)
	predictedState.AddVec(predictedState, &aDX)
	predictedState.AddVec(predictedState, &bDU)
	predictedOutput := mat.VecDenseCopyOf(h0)
	var cDX, dDU mat.VecDense
	cDX.MulVec(system.C, dx)
	dDU.MulVec(system.D, du)
	predictedOutput.AddVec(predictedOutput, &cDX)
	predictedOutput.AddVec(predictedOutput, &dDU)
	actualState.SubVec(actualState, predictedState)
	actualOutput.SubVec(actualOutput, predictedOutput)
	return vectorNorm(actualState), vectorNorm(actualOutput), nil
}

func validatePSDCovariance(name string, matrix *mat.Dense, size int) error {
	if matrix == nil {
		return invalid("%s is required", name)
	}
	rows, columns := matrix.Dims()
	if rows != size || columns != size {
		return invalid("%s is %dx%d; want %dx%d", name, rows, columns, size, size)
	}
	scale := 1.0
	for row := range rows {
		for column := range columns {
			value := matrix.At(row, column)
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return invalid("%s contains a non-finite value", name)
			}
			scale = math.Max(scale, math.Abs(value))
		}
	}
	tolerance := 1e-12 * scale
	for row := range rows {
		for column := row + 1; column < columns; column++ {
			if math.Abs(matrix.At(row, column)-matrix.At(column, row)) > tolerance {
				return invalid("%s must be symmetric", name)
			}
		}
	}
	symmetric := mat.NewSymDense(size, nil)
	for row := range rows {
		for column := 0; column <= row; column++ {
			symmetric.SetSym(row, column, matrix.At(row, column))
		}
	}
	var decomposition mat.EigenSym
	if ok := decomposition.Factorize(symmetric, false); !ok {
		return invalid("%s symmetric eigendecomposition failed", name)
	}
	for _, value := range decomposition.Values(nil) {
		if value < -tolerance {
			return invalid("%s must be positive semidefinite", name)
		}
	}
	return nil
}

func validateFiniteVector(name string, values []float64, size int) error {
	if len(values) != size {
		return invalid("%s has length %d; want %d", name, len(values), size)
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return invalid("%s contains a non-finite value", name)
		}
	}
	return nil
}

func checkedVector(
	name string,
	vector *mat.VecDense,
	size int,
) (*mat.VecDense, error) {
	if vector == nil {
		return nil, invalid("%s callback returned nil", name)
	}
	if vector.Len() != size {
		return nil, invalid(
			"%s callback returned length %d; want %d", name, vector.Len(), size,
		)
	}
	values := vectorValues(vector)
	if err := validateFiniteVector(name, values, size); err != nil {
		return nil, err
	}
	return mat.NewVecDense(size, values), nil
}

func matrixValueFromDense(matrix *mat.Dense) (MatrixValue, error) {
	rows, columns := matrix.Dims()
	values := make([]float64, 0, rows*columns)
	for row := range rows {
		for column := range columns {
			values = append(values, matrix.At(row, column))
		}
	}
	return NewMatrixValue(rows, columns, values)
}

func vectorValues(vector *mat.VecDense) []float64 {
	values := make([]float64, vector.Len())
	for i := range values {
		values[i] = vector.AtVec(i)
	}
	return values
}

func vectorNorm(vector *mat.VecDense) float64 {
	return mat.Norm(vector, 2)
}

func scaledVector(values []float64, scale float64) *mat.VecDense {
	scaled := make([]float64, len(values))
	for i, value := range values {
		scaled[i] = value * scale
	}
	return mat.NewVecDense(len(scaled), scaled)
}

func combinedDirectionNorm(state, input []float64) float64 {
	var squared float64
	for _, value := range state {
		squared += value * value
	}
	for _, value := range input {
		squared += value * value
	}
	return math.Sqrt(squared)
}

func finiteErrorRatio(full, half float64) *float64 {
	if half <= 1e-15 || math.IsNaN(full) || math.IsInf(full, 0) {
		return nil
	}
	ratio := full / half
	return &ratio
}

func cloneNonlinearDefinition(definition NonlinearDefinition) NonlinearDefinition {
	definition.StateNames = append([]string(nil), definition.StateNames...)
	definition.InputNames = append([]string(nil), definition.InputNames...)
	definition.OutputNames = append([]string(nil), definition.OutputNames...)
	return definition
}

func cloneNamedOperatingPoint(point NamedOperatingPoint) NamedOperatingPoint {
	point.State = append([]float64(nil), point.State...)
	point.Input = append([]float64(nil), point.Input...)
	return point
}
