package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

const controlRoleSpecVersion = 1

type ChannelDirection string

const (
	ChannelInput  ChannelDirection = "input"
	ChannelOutput ChannelDirection = "output"
)

type NamedChannelRef struct {
	BlockID     int64            `json:"blockId"`
	Direction   ChannelDirection `json:"direction"`
	Port        int              `json:"port"`
	ChannelName string           `json:"channelName"`
}

type PlantRole struct {
	Blocks             []int64           `json:"blocks"`
	ExogenousInputs    []NamedChannelRef `json:"exogenousInputs"`
	ControlInputs      []NamedChannelRef `json:"controlInputs"`
	PerformanceOutputs []NamedChannelRef `json:"performanceOutputs"`
	MeasurementOutputs []NamedChannelRef `json:"measurementOutputs"`
}

type ControllerRole struct {
	Blocks            []int64           `json:"blocks"`
	ReferenceInputs   []NamedChannelRef `json:"referenceInputs,omitempty"`
	MeasurementInputs []NamedChannelRef `json:"measurementInputs"`
	ControlOutputs    []NamedChannelRef `json:"controlOutputs"`
}

type AnalysisPointLocation string

const (
	AnalysisPointPlantInput  AnalysisPointLocation = "plant_input"
	AnalysisPointPlantOutput AnalysisPointLocation = "plant_output"
)

type LoopBreakPair struct {
	Output NamedChannelRef `json:"output"`
	Input  NamedChannelRef `json:"input"`
}

type AnalysisPointRole struct {
	Name     string                `json:"name"`
	Location AnalysisPointLocation `json:"location"`
	Pairs    []LoopBreakPair       `json:"pairs"`
}

type ControlRoleSpec struct {
	Version        int                 `json:"version"`
	Plant          PlantRole           `json:"plant"`
	Controller     ControllerRole      `json:"controller"`
	AnalysisPoints []AnalysisPointRole `json:"analysisPoints"`
}

type ControlModelBuildRequest struct {
	BaseStep float64
}

type ControlPointModels struct {
	Name       string
	Location   AnalysisPointLocation
	OpenLoop   *controlsys.System
	ClosedLoop *controlsys.System
}

type ControlModelSet struct {
	Plant               *controlsys.System
	Controller          *controlsys.System
	ReferenceController *controlsys.System
	GeneralizedPlant    *controlsys.System
	EstimatorPlant      *controlsys.System
	Loop                *controlsys.GeneralizedClosedLoop
	Points              []ControlPointModels
}

func (s *Studio) AssignControlRoles(
	ctx context.Context,
	flowID int64,
	spec ControlRoleSpec,
) (ControlRoleSpec, error) {
	spec = normalizeControlRoleSpec(spec)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		blocks, connections, err := loadControlRoleGraph(ctx, tx, flowID)
		if err != nil {
			return err
		}
		if _, err := resolveControlRoleSpec(blocks, connections, spec); err != nil {
			return err
		}
		if err := storeControlRoleSpec(ctx, tx, flowID, spec); err != nil {
			return err
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx,
			"UPDATE flows SET updated_at = ? WHERE id = ?", now, flowID,
		); err != nil {
			return err
		}
		return insertEvent(ctx, tx, flowID, now, "Updated control model roles")
	})
	if err != nil {
		return ControlRoleSpec{}, err
	}
	return cloneControlRoleSpec(spec), nil
}

func (s *Studio) ControlRoles(ctx context.Context, flowID int64) (ControlRoleSpec, error) {
	spec, err := loadControlRoleSpec(ctx, s.db, flowID)
	if err != nil {
		return ControlRoleSpec{}, err
	}
	return cloneControlRoleSpec(spec), nil
}

func (s *Studio) BuildControlModels(
	ctx context.Context,
	flowID int64,
	request ControlModelBuildRequest,
) (ControlModelSet, error) {
	if request.BaseStep < 0 {
		return ControlModelSet{}, invalid("control model base step cannot be negative")
	}
	spec, err := loadControlRoleSpec(ctx, s.db, flowID)
	if err != nil {
		return ControlModelSet{}, err
	}
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return ControlModelSet{}, err
	}
	resolved, err := resolveControlRoleSpec(snapshot.Blocks, snapshot.Connections, spec)
	if err != nil {
		return ControlModelSet{}, err
	}
	return buildControlModels(snapshot, resolved, request)
}

type resolvedNamedChannel struct {
	Named NamedChannelRef
	Ref   ChannelRef
}

type resolvedPlantRole struct {
	Blocks             []int64
	ExogenousInputs    []resolvedNamedChannel
	ControlInputs      []resolvedNamedChannel
	PerformanceOutputs []resolvedNamedChannel
	MeasurementOutputs []resolvedNamedChannel
}

type resolvedControllerRole struct {
	Blocks            []int64
	ReferenceInputs   []resolvedNamedChannel
	MeasurementInputs []resolvedNamedChannel
	ControlOutputs    []resolvedNamedChannel
}

type resolvedControlRoleSpec struct {
	Plant          resolvedPlantRole
	Controller     resolvedControllerRole
	AnalysisPoints []AnalysisPointRole
}

func resolveControlRoleSpec(
	blocks []Block,
	connections []Connection,
	spec ControlRoleSpec,
) (resolvedControlRoleSpec, error) {
	if spec.Version != controlRoleSpecVersion {
		return resolvedControlRoleSpec{}, invalid(
			"control role specification version %d is unsupported", spec.Version,
		)
	}
	blockByID := make(map[int64]Block, len(blocks))
	for _, block := range blocks {
		blockByID[block.ID] = block
	}
	plantSet, err := validateSubsystemBlocks("plant", spec.Plant.Blocks, blockByID)
	if err != nil {
		return resolvedControlRoleSpec{}, err
	}
	controllerSet, err := validateSubsystemBlocks(
		"controller", spec.Controller.Blocks, blockByID,
	)
	if err != nil {
		return resolvedControlRoleSpec{}, err
	}
	for blockID := range plantSet {
		if _, duplicate := controllerSet[blockID]; duplicate {
			return resolvedControlRoleSpec{}, invalid(
				"block %d belongs to both the plant and controller", blockID,
			)
		}
	}

	resolve := func(
		role string,
		refs []NamedChannelRef,
		direction ChannelDirection,
		members map[int64]struct{},
	) ([]resolvedNamedChannel, error) {
		resolved := make([]resolvedNamedChannel, len(refs))
		seen := make(map[NamedChannelRef]struct{}, len(refs))
		for i, ref := range refs {
			if ref.Direction != direction {
				return nil, invalid(
					"%s %d must reference a %s channel", role, i+1, direction,
				)
			}
			if _, member := members[ref.BlockID]; !member {
				return nil, invalid(
					"%s %d references block %d outside its subsystem",
					role, i+1, ref.BlockID,
				)
			}
			if _, duplicate := seen[ref]; duplicate {
				return nil, invalid(
					"%s assigns %s more than once", role, namedRefLabel(ref),
				)
			}
			seen[ref] = struct{}{}
			block := blockByID[ref.BlockID]
			channel, err := resolveNamedChannel(block, ref)
			if err != nil {
				return nil, invalid("%s %d: %s", role, i+1, err)
			}
			resolved[i] = resolvedNamedChannel{Named: ref, Ref: channel}
		}
		return resolved, nil
	}

	var result resolvedControlRoleSpec
	result.Plant.Blocks = append([]int64(nil), spec.Plant.Blocks...)
	result.Controller.Blocks = append([]int64(nil), spec.Controller.Blocks...)
	if result.Plant.ExogenousInputs, err = resolve(
		"plant exogenous input", spec.Plant.ExogenousInputs, ChannelInput, plantSet,
	); err != nil {
		return resolvedControlRoleSpec{}, err
	}
	if result.Plant.ControlInputs, err = resolve(
		"plant control input", spec.Plant.ControlInputs, ChannelInput, plantSet,
	); err != nil {
		return resolvedControlRoleSpec{}, err
	}
	if result.Plant.PerformanceOutputs, err = resolve(
		"plant performance output", spec.Plant.PerformanceOutputs, ChannelOutput, plantSet,
	); err != nil {
		return resolvedControlRoleSpec{}, err
	}
	if result.Plant.MeasurementOutputs, err = resolve(
		"plant measurement output", spec.Plant.MeasurementOutputs, ChannelOutput, plantSet,
	); err != nil {
		return resolvedControlRoleSpec{}, err
	}
	if result.Controller.MeasurementInputs, err = resolve(
		"controller measurement input",
		spec.Controller.MeasurementInputs,
		ChannelInput,
		controllerSet,
	); err != nil {
		return resolvedControlRoleSpec{}, err
	}
	if result.Controller.ReferenceInputs, err = resolve(
		"controller reference input",
		spec.Controller.ReferenceInputs,
		ChannelInput,
		controllerSet,
	); err != nil {
		return resolvedControlRoleSpec{}, err
	}
	if result.Controller.ControlOutputs, err = resolve(
		"controller control output",
		spec.Controller.ControlOutputs,
		ChannelOutput,
		controllerSet,
	); err != nil {
		return resolvedControlRoleSpec{}, err
	}
	if len(result.Plant.ControlInputs) == 0 {
		return resolvedControlRoleSpec{}, invalid("assign at least one plant control input")
	}
	if len(result.Plant.MeasurementOutputs) == 0 {
		return resolvedControlRoleSpec{}, invalid("assign at least one plant measurement output")
	}
	if len(result.Plant.ControlInputs) != len(result.Controller.ControlOutputs) {
		return resolvedControlRoleSpec{}, invalid(
			"plant has %d control inputs but controller has %d control outputs",
			len(result.Plant.ControlInputs), len(result.Controller.ControlOutputs),
		)
	}
	if len(result.Plant.MeasurementOutputs) != len(result.Controller.MeasurementInputs) {
		return resolvedControlRoleSpec{}, invalid(
			"plant has %d measurement outputs but controller has %d measurement inputs",
			len(result.Plant.MeasurementOutputs), len(result.Controller.MeasurementInputs),
		)
	}
	if len(result.Controller.ReferenceInputs) != 0 &&
		len(result.Controller.ReferenceInputs) != len(result.Plant.MeasurementOutputs) {
		return resolvedControlRoleSpec{}, invalid(
			"controller has %d reference inputs but plant has %d measurement outputs",
			len(result.Controller.ReferenceInputs), len(result.Plant.MeasurementOutputs),
		)
	}
	for label, channels := range map[string][]resolvedNamedChannel{
		"generalized plant inputs": appendResolved(
			result.Plant.ExogenousInputs, result.Plant.ControlInputs,
		),
		"generalized plant outputs": appendResolved(
			result.Plant.PerformanceOutputs, result.Plant.MeasurementOutputs,
		),
		"controller inputs": appendResolved(
			result.Controller.ReferenceInputs, result.Controller.MeasurementInputs,
		),
		"controller outputs": result.Controller.ControlOutputs,
	} {
		if err := validateUniqueControlSignalNames(label, channels); err != nil {
			return resolvedControlRoleSpec{}, err
		}
	}
	if err := validateBoundaryPorts(
		"plant inputs",
		blockByID,
		appendResolved(result.Plant.ExogenousInputs, result.Plant.ControlInputs),
	); err != nil {
		return resolvedControlRoleSpec{}, err
	}
	if err := validateBoundaryPorts(
		"controller inputs",
		blockByID,
		appendResolved(
			result.Controller.ReferenceInputs, result.Controller.MeasurementInputs,
		),
	); err != nil {
		return resolvedControlRoleSpec{}, err
	}
	if err := validateControlAnalysisPoints(
		spec.AnalysisPoints, spec, blockByID, connections,
	); err != nil {
		return resolvedControlRoleSpec{}, err
	}
	result.AnalysisPoints = cloneAnalysisPoints(spec.AnalysisPoints)
	for i := range result.AnalysisPoints {
		result.AnalysisPoints[i].Name = strings.TrimSpace(result.AnalysisPoints[i].Name)
	}
	return result, nil
}

func validateSubsystemBlocks(
	name string,
	blockIDs []int64,
	blockByID map[int64]Block,
) (map[int64]struct{}, error) {
	if len(blockIDs) == 0 {
		return nil, invalid("assign at least one %s block", name)
	}
	members := make(map[int64]struct{}, len(blockIDs))
	for _, blockID := range blockIDs {
		block, ok := blockByID[blockID]
		if !ok {
			return nil, invalid("%s references missing block %d", name, blockID)
		}
		if _, duplicate := members[blockID]; duplicate {
			return nil, invalid("%s block %d is assigned more than once", name, blockID)
		}
		if block.Kind.isSource() || block.Kind.isSink() {
			return nil, invalid(
				"%s block %s must be dynamic or algebraic, not a source or sink",
				name, block.Name,
			)
		}
		members[blockID] = struct{}{}
	}
	return members, nil
}

func resolveNamedChannel(block Block, ref NamedChannelRef) (ChannelRef, error) {
	var (
		port SignalPort
		ok   bool
	)
	switch ref.Direction {
	case ChannelInput:
		port, ok = resolvedInputPort(block, ref.Port)
	case ChannelOutput:
		port, ok = block.OutputPort(ref.Port)
	default:
		return ChannelRef{}, invalid("channel direction %q is unknown", ref.Direction)
	}
	if !ok {
		return ChannelRef{}, invalid(
			"%s has no %s port %d", block.Name, ref.Direction, ref.Port,
		)
	}
	for channel, name := range port.Channels {
		if name == ref.ChannelName {
			return ChannelRef{
				BlockID: ref.BlockID, Port: ref.Port, Channel: channel,
			}, nil
		}
	}
	return ChannelRef{}, invalid(
		"%s %s port %d no longer has named channel %q",
		block.Name, ref.Direction, ref.Port, ref.ChannelName,
	)
}

func validateBoundaryPorts(
	role string,
	blockByID map[int64]Block,
	channels []resolvedNamedChannel,
) error {
	type portKey struct {
		blockID int64
		port    int
	}
	assigned := make(map[portKey]map[int]struct{})
	for _, channel := range channels {
		key := portKey{blockID: channel.Ref.BlockID, port: channel.Ref.Port}
		if assigned[key] == nil {
			assigned[key] = make(map[int]struct{})
		}
		if _, duplicate := assigned[key][channel.Ref.Channel]; duplicate {
			return invalid("%s assigns %s more than once", role, namedRefLabel(channel.Named))
		}
		assigned[key][channel.Ref.Channel] = struct{}{}
	}
	for key, selected := range assigned {
		port, _ := resolvedInputPort(blockByID[key.blockID], key.port)
		if len(selected) != port.Width {
			return invalid(
				"%s must expose every channel of block %d input port %d; got %d of %d",
				role, key.blockID, key.port, len(selected), port.Width,
			)
		}
	}
	return nil
}

func validateUniqueControlSignalNames(
	role string,
	channels []resolvedNamedChannel,
) error {
	seen := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		name := channel.Named.ChannelName
		if _, duplicate := seen[name]; duplicate {
			return invalid("%s use named channel %q more than once", role, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateControlAnalysisPoints(
	points []AnalysisPointRole,
	spec ControlRoleSpec,
	blockByID map[int64]Block,
	connections []Connection,
) error {
	if len(points) == 0 {
		return invalid("assign at least one control analysis point")
	}
	names := make(map[string]struct{}, len(points))
	for i, point := range points {
		point.Name = strings.TrimSpace(point.Name)
		if point.Name == "" {
			return invalid("analysis point %d must have a name", i+1)
		}
		if _, duplicate := names[point.Name]; duplicate {
			return invalid("analysis point %q is assigned more than once", point.Name)
		}
		names[point.Name] = struct{}{}
		var wantOutputs, wantInputs []NamedChannelRef
		switch point.Location {
		case AnalysisPointPlantInput:
			wantOutputs = spec.Controller.ControlOutputs
			wantInputs = spec.Plant.ControlInputs
		case AnalysisPointPlantOutput:
			wantOutputs = spec.Plant.MeasurementOutputs
			wantInputs = spec.Controller.MeasurementInputs
		default:
			return invalid(
				"analysis point %q has unknown location %q", point.Name, point.Location,
			)
		}
		if len(point.Pairs) != len(wantOutputs) {
			return invalid(
				"analysis point %q has %d channel pairs; want %d",
				point.Name, len(point.Pairs), len(wantOutputs),
			)
		}
		for pairIndex, pair := range point.Pairs {
			if pair.Output != wantOutputs[pairIndex] || pair.Input != wantInputs[pairIndex] {
				return invalid(
					"analysis point %q pair %d does not match the ordered %s boundary",
					point.Name, pairIndex+1, point.Location,
				)
			}
			outputBlock := blockByID[pair.Output.BlockID]
			inputBlock := blockByID[pair.Input.BlockID]
			outputRef, err := resolveNamedChannel(outputBlock, pair.Output)
			if err != nil {
				return invalid("analysis point %q output: %s", point.Name, err)
			}
			inputRef, err := resolveNamedChannel(inputBlock, pair.Input)
			if err != nil {
				return invalid("analysis point %q input: %s", point.Name, err)
			}
			if outputRef.Channel != inputRef.Channel {
				return invalid(
					"analysis point %q pair %d names different physical channel indices",
					point.Name, pairIndex+1,
				)
			}
			if !connectionExists(
				connections,
				outputRef.BlockID,
				outputRef.Port,
				inputRef.BlockID,
				inputRef.Port,
			) {
				return invalid(
					"analysis point %q pair %d is not a drawn connection",
					point.Name, pairIndex+1,
				)
			}
		}
	}
	return nil
}

func buildControlModels(
	snapshot Snapshot,
	spec resolvedControlRoleSpec,
	request ControlModelBuildRequest,
) (ControlModelSet, error) {
	plantInputs := appendResolved(spec.Plant.ExogenousInputs, spec.Plant.ControlInputs)
	plantOutputs := appendResolved(
		spec.Plant.PerformanceOutputs, spec.Plant.MeasurementOutputs,
	)
	generalized, err := compileControlSubsystem(
		snapshot, spec.Plant.Blocks, plantInputs, plantOutputs, request.BaseStep,
	)
	if err != nil {
		return ControlModelSet{}, fmt.Errorf("build generalized plant: %w", err)
	}
	plant, err := selectSystemChannels(
		generalized,
		namedChannelNames(spec.Plant.ControlInputs),
		namedChannelNames(spec.Plant.MeasurementOutputs),
	)
	if err != nil {
		return ControlModelSet{}, fmt.Errorf("select plant control model: %w", err)
	}
	controllerInputs := appendResolved(
		spec.Controller.ReferenceInputs, spec.Controller.MeasurementInputs,
	)
	referenceController, err := compileControlSubsystem(
		snapshot,
		spec.Controller.Blocks,
		controllerInputs,
		spec.Controller.ControlOutputs,
		request.BaseStep,
	)
	if err != nil {
		return ControlModelSet{}, fmt.Errorf("build controller: %w", err)
	}
	if plant.IsDiscrete() &&
		subsystemUsesOnlyNeutralTimeDomain(snapshot.Blocks, spec.Controller.Blocks) {
		referenceController.Dt = plant.Dt
	}
	controller, err := selectSystemChannels(
		referenceController,
		namedChannelNames(spec.Controller.MeasurementInputs),
		namedChannelNames(spec.Controller.ControlOutputs),
	)
	if err != nil {
		return ControlModelSet{}, fmt.Errorf("select controller feedback model: %w", err)
	}
	if len(spec.Controller.ReferenceInputs) != 0 {
		controller, err = negateSystemOutputs(controller)
		if err != nil {
			return ControlModelSet{}, fmt.Errorf("normalize controller feedback sign: %w", err)
		}
	}
	if err := validateControlModelDomains(plant, controller); err != nil {
		return ControlModelSet{}, err
	}
	primary := spec.AnalysisPoints[0]
	loop, err := controlsys.NewGeneralizedClosedLoop(
		"flowsheet-control-loop", plant, controller, primary.Name,
	)
	if err != nil {
		return ControlModelSet{}, fmt.Errorf("assemble generalized closed loop: %w", err)
	}
	if primary.Location == AnalysisPointPlantInput {
		if err := loop.InsertAnalysisPoint(
			primary.Name, controlsys.AnalysisPointPlantInput,
		); err != nil {
			return ControlModelSet{}, err
		}
	}
	for _, point := range spec.AnalysisPoints[1:] {
		location := controlsys.AnalysisPointPlantOutput
		if point.Location == AnalysisPointPlantInput {
			location = controlsys.AnalysisPointPlantInput
		}
		if err := loop.InsertAnalysisPoint(point.Name, location); err != nil {
			return ControlModelSet{}, fmt.Errorf(
				"add analysis point %q: %w", point.Name, err,
			)
		}
	}
	result := ControlModelSet{
		Plant:               plant,
		Controller:          controller,
		ReferenceController: referenceController,
		GeneralizedPlant:    generalized,
		EstimatorPlant:      plant.Copy(),
		Loop:                loop,
		Points:              make([]ControlPointModels, len(spec.AnalysisPoints)),
	}
	for i, point := range spec.AnalysisPoints {
		openLoop, closedLoop, err := controlPointSystems(loop, point.Name)
		if err != nil {
			return ControlModelSet{}, err
		}
		result.Points[i] = ControlPointModels{
			Name: point.Name, Location: point.Location,
			OpenLoop: openLoop, ClosedLoop: closedLoop,
		}
	}
	return result, nil
}

func negateSystemOutputs(system *controlsys.System) (*controlsys.System, error) {
	_, _, outputs := system.Dims()
	data := make([]float64, outputs*outputs)
	for i := range outputs {
		data[i*outputs+i] = -1
	}
	negation, err := controlsys.NewGain(
		mat.NewDense(outputs, outputs, data),
		system.Dt,
	)
	if err != nil {
		return nil, err
	}
	result, err := controlsys.Series(system, negation)
	if err != nil {
		return nil, err
	}
	if err := result.SetInputName(system.InputName...); err != nil {
		return nil, err
	}
	if err := result.SetOutputName(system.OutputName...); err != nil {
		return nil, err
	}
	return result, nil
}

func subsystemUsesOnlyNeutralTimeDomain(blocks []Block, blockIDs []int64) bool {
	selected := make(map[int64]struct{}, len(blockIDs))
	for _, blockID := range blockIDs {
		selected[blockID] = struct{}{}
	}
	for _, block := range blocks {
		if _, ok := selected[block.ID]; !ok {
			continue
		}
		if blockDefinitions[block.Kind].domain(block.Parameters).kind != timeDomainNeutral {
			return false
		}
	}
	return true
}

func controlPointSystems(
	loop *controlsys.GeneralizedClosedLoop,
	name string,
) (openLoop, closedLoop *controlsys.System, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			openLoop = nil
			closedLoop = nil
			err = fmt.Errorf(
				"build control models at %q: controlsys failed: %v",
				name, recovered,
			)
		}
	}()
	openLoop, err = loop.OpenLoop(name)
	if err != nil {
		return nil, nil, fmt.Errorf("build open loop at %q: %w", name, err)
	}
	closedLoop, err = loop.ClosedLoop(name)
	if err != nil {
		return nil, nil, fmt.Errorf("build closed loop at %q: %w", name, err)
	}
	return openLoop, closedLoop, nil
}

func compileControlSubsystem(
	snapshot Snapshot,
	blockIDs []int64,
	inputs []resolvedNamedChannel,
	outputs []resolvedNamedChannel,
	baseStep float64,
) (*controlsys.System, error) {
	members := make(map[int64]struct{}, len(blockIDs))
	for _, blockID := range blockIDs {
		members[blockID] = struct{}{}
	}
	blockByID := make(map[int64]Block, len(snapshot.Blocks))
	var blocks []Block
	for _, block := range snapshot.Blocks {
		blockByID[block.ID] = block
		if _, member := members[block.ID]; member {
			blocks = append(blocks, block)
		}
	}
	var connections []Connection
	for _, connection := range snapshot.Connections {
		_, sourceMember := members[connection.SourceID]
		_, targetMember := members[connection.TargetID]
		if sourceMember && targetMember {
			connections = append(connections, connection)
		}
	}

	type portKey struct {
		blockID int64
		port    int
	}
	groups := make(map[portKey][]resolvedNamedChannel)
	var order []portKey
	for _, input := range inputs {
		key := portKey{blockID: input.Ref.BlockID, port: input.Ref.Port}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], input)
	}
	syntheticRefs := make(map[NamedChannelRef]ChannelRef, len(inputs))
	const syntheticBaseID int64 = -1_000_000_000
	for groupIndex, key := range order {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool {
			return group[i].Ref.Channel < group[j].Ref.Channel
		})
		names := make([]string, len(group))
		values := make([]float64, len(group))
		for i, channel := range group {
			names[i] = channel.Named.ChannelName
		}
		vector, _ := NewVectorValue(values)
		channelNames, _ := NewChannelNames(names)
		sourceID := syntheticBaseID + int64(groupIndex)
		source := Block{
			ID: sourceID, FlowID: snapshot.Flow.ID,
			Kind: BlockVectorConstant, Name: fmt.Sprintf("Control boundary %d", groupIndex+1),
			Parameters: Parameters{Vector: &vector, OutputNames: &channelNames},
		}
		blocks = append(blocks, source)
		connections = append(connections, Connection{
			FlowID:   snapshot.Flow.ID,
			SourceID: sourceID, SourcePort: 0,
			TargetID: key.blockID, TargetPort: key.port,
		})
		for channel, resolved := range group {
			syntheticRefs[resolved.Named] = ChannelRef{
				BlockID: sourceID, Port: 0, Channel: channel,
			}
		}
	}
	probes := make([]modelProbe, 0, len(outputs))
	for _, output := range outputs {
		probes = append(probes, modelProbe{
			BlockID: output.Ref.BlockID, OutputPort: output.Ref.Port,
		})
	}
	model, err := compileRequestedModel(
		blocks,
		connections,
		modelCompileRequest{probes: probes, baseStep: baseStep},
	)
	if err != nil {
		return nil, err
	}
	inputRefs := make([]ChannelRef, len(inputs))
	for i, input := range inputs {
		inputRefs[i] = syntheticRefs[input.Named]
	}
	outputRefs := make([]ChannelRef, len(outputs))
	for i, output := range outputs {
		outputRefs[i] = output.Ref
	}
	system, _, _, err := model.selectChannels(inputRefs, outputRefs)
	if err != nil {
		return nil, err
	}
	if err := system.SetInputName(namedChannelNames(inputs)...); err != nil {
		return nil, err
	}
	if err := system.SetOutputName(namedChannelNames(outputs)...); err != nil {
		return nil, err
	}
	return system, nil
}

func validateControlModelDomains(plant, controller *controlsys.System) error {
	if plant.IsDiscrete() != controller.IsDiscrete() {
		return invalid("plant and controller use different time domains")
	}
	if plant.IsDiscrete() &&
		math.Abs(plant.Dt-controller.Dt) > 1e-12*math.Max(1, math.Max(plant.Dt, controller.Dt)) {
		return invalid(
			"plant sample time %.12g does not match controller sample time %.12g",
			plant.Dt, controller.Dt,
		)
	}
	_, plantInputs, plantOutputs := plant.Dims()
	_, controllerInputs, controllerOutputs := controller.Dims()
	if plantInputs != controllerOutputs || plantOutputs != controllerInputs {
		return invalid(
			"plant %d×%d and controller %d×%d dimensions do not form a feedback loop",
			plantOutputs, plantInputs, controllerOutputs, controllerInputs,
		)
	}
	return nil
}

func appendResolved(groups ...[]resolvedNamedChannel) []resolvedNamedChannel {
	var result []resolvedNamedChannel
	for _, group := range groups {
		result = append(result, group...)
	}
	return result
}

func namedChannelNames(channels []resolvedNamedChannel) []string {
	names := make([]string, len(channels))
	for i, channel := range channels {
		names[i] = channel.Named.ChannelName
	}
	return names
}

func namedRefLabel(ref NamedChannelRef) string {
	return fmt.Sprintf(
		"block %d %s port %d channel %q",
		ref.BlockID, ref.Direction, ref.Port, ref.ChannelName,
	)
}

func connectionExists(
	connections []Connection,
	sourceID int64,
	sourcePort int,
	targetID int64,
	targetPort int,
) bool {
	for _, connection := range connections {
		if connection.SourceID == sourceID &&
			connection.SourcePort == sourcePort &&
			connection.TargetID == targetID &&
			connection.TargetPort == targetPort {
			return true
		}
	}
	return false
}

func cloneAnalysisPoints(points []AnalysisPointRole) []AnalysisPointRole {
	cloned := make([]AnalysisPointRole, len(points))
	for i, point := range points {
		cloned[i] = point
		cloned[i].Pairs = append([]LoopBreakPair(nil), point.Pairs...)
	}
	return cloned
}

func cloneControlRoleSpec(spec ControlRoleSpec) ControlRoleSpec {
	encoded, _ := json.Marshal(spec)
	var cloned ControlRoleSpec
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func normalizeControlRoleSpec(spec ControlRoleSpec) ControlRoleSpec {
	spec = cloneControlRoleSpec(spec)
	spec.Version = controlRoleSpecVersion
	for i := range spec.AnalysisPoints {
		spec.AnalysisPoints[i].Name = strings.TrimSpace(spec.AnalysisPoints[i].Name)
	}
	return spec
}

type controlSpecQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadControlRoleSpec(
	ctx context.Context,
	queryer controlSpecQueryer,
	flowID int64,
) (ControlRoleSpec, error) {
	var version int
	var encoded string
	err := queryer.QueryRowContext(ctx, `
		SELECT version, spec_json
		FROM control_model_specs
		WHERE flow_id = ?`, flowID,
	).Scan(&version, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return ControlRoleSpec{}, invalid("assign plant and controller roles before building control models")
	}
	if err != nil {
		return ControlRoleSpec{}, fmt.Errorf("load control roles: %w", err)
	}
	var spec ControlRoleSpec
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
		return ControlRoleSpec{}, fmt.Errorf("decode control roles: %w", err)
	}
	if version != spec.Version {
		return ControlRoleSpec{}, invalid(
			"control role storage version %d does not match payload version %d",
			version, spec.Version,
		)
	}
	return spec, nil
}

func storeControlRoleSpec(
	ctx context.Context,
	tx *sql.Tx,
	flowID int64,
	spec ControlRoleSpec,
) error {
	encoded, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("encode control roles: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO control_model_specs(flow_id, version, spec_json)
		VALUES(?, ?, ?)
		ON CONFLICT(flow_id) DO UPDATE SET
			version = excluded.version,
			spec_json = excluded.spec_json`,
		flowID, spec.Version, string(encoded),
	); err != nil {
		return fmt.Errorf("store control roles: %w", err)
	}
	return nil
}

func loadControlRoleGraph(
	ctx context.Context,
	tx *sql.Tx,
	flowID int64,
) ([]Block, []Connection, error) {
	var exists int
	if err := tx.QueryRowContext(ctx,
		"SELECT 1 FROM flows WHERE id = ?", flowID,
	).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, flow_id, kind, name, x, y, parameters_json
		FROM blocks WHERE flow_id = ? ORDER BY id`, flowID)
	if err != nil {
		return nil, nil, err
	}
	var blocks []Block
	for rows.Next() {
		var block Block
		var encoded string
		if err := rows.Scan(
			&block.ID, &block.FlowID, &block.Kind, &block.Name,
			&block.Position.X, &block.Position.Y, &encoded,
		); err != nil {
			rows.Close()
			return nil, nil, err
		}
		block.Parameters, err = decodeParameters(block.Kind, encoded)
		if err != nil {
			rows.Close()
			return nil, nil, err
		}
		blocks = append(blocks, block)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	rows, err = tx.QueryContext(ctx, `
		SELECT id, flow_id, source_id, source_port, target_id, target_port
		FROM connections WHERE flow_id = ? ORDER BY id`, flowID)
	if err != nil {
		return nil, nil, err
	}
	var connections []Connection
	for rows.Next() {
		var connection Connection
		if err := rows.Scan(
			&connection.ID, &connection.FlowID,
			&connection.SourceID, &connection.SourcePort,
			&connection.TargetID, &connection.TargetPort,
		); err != nil {
			rows.Close()
			return nil, nil, err
		}
		connections = append(connections, connection)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	return blocks, connections, nil
}

func clearControlRoleSpecForBlocks(
	ctx context.Context,
	tx *sql.Tx,
	flowID int64,
	blockIDs []int64,
) error {
	var encoded string
	err := tx.QueryRowContext(ctx,
		"SELECT spec_json FROM control_model_specs WHERE flow_id = ?", flowID,
	).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load control roles before deleting blocks: %w", err)
	}
	var spec ControlRoleSpec
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
		return fmt.Errorf("decode control roles before deleting blocks: %w", err)
	}
	deleted := make(map[int64]struct{}, len(blockIDs))
	for _, blockID := range blockIDs {
		deleted[blockID] = struct{}{}
	}
	for _, blockID := range controlRoleBlockIDs(spec) {
		if _, referenced := deleted[blockID]; !referenced {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM control_model_specs WHERE flow_id = ?", flowID,
		); err != nil {
			return fmt.Errorf("clear control roles for deleted block: %w", err)
		}
		return nil
	}
	return nil
}

func copyControlRoleSpec(
	ctx context.Context,
	tx *sql.Tx,
	sourceFlowID int64,
	targetFlowID int64,
	moved map[int64]int64,
) error {
	var version int
	var encoded string
	err := tx.QueryRowContext(ctx, `
		SELECT version, spec_json
		FROM control_model_specs
		WHERE flow_id = ?`, sourceFlowID,
	).Scan(&version, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load control roles to copy: %w", err)
	}
	var spec ControlRoleSpec
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
		return fmt.Errorf("decode control roles to copy: %w", err)
	}
	if version != spec.Version {
		return invalid(
			"control role storage version %d does not match payload version %d",
			version, spec.Version,
		)
	}
	remap := func(blockID int64) (int64, error) {
		copied, ok := moved[blockID]
		if !ok {
			return 0, fmt.Errorf(
				"control role block %d is not part of flowsheet %d",
				blockID, sourceFlowID,
			)
		}
		return copied, nil
	}
	for i, blockID := range spec.Plant.Blocks {
		copied, err := remap(blockID)
		if err != nil {
			return err
		}
		spec.Plant.Blocks[i] = copied
	}
	for i, blockID := range spec.Controller.Blocks {
		copied, err := remap(blockID)
		if err != nil {
			return err
		}
		spec.Controller.Blocks[i] = copied
	}
	var remapRefs func([]NamedChannelRef) error
	remapRefs = func(refs []NamedChannelRef) error {
		for i := range refs {
			copied, err := remap(refs[i].BlockID)
			if err != nil {
				return err
			}
			refs[i].BlockID = copied
		}
		return nil
	}
	for _, refs := range [][]NamedChannelRef{
		spec.Plant.ExogenousInputs,
		spec.Plant.ControlInputs,
		spec.Plant.PerformanceOutputs,
		spec.Plant.MeasurementOutputs,
		spec.Controller.ReferenceInputs,
		spec.Controller.MeasurementInputs,
		spec.Controller.ControlOutputs,
	} {
		if err := remapRefs(refs); err != nil {
			return err
		}
	}
	for pointIndex := range spec.AnalysisPoints {
		for pairIndex := range spec.AnalysisPoints[pointIndex].Pairs {
			pair := &spec.AnalysisPoints[pointIndex].Pairs[pairIndex]
			outputID, err := remap(pair.Output.BlockID)
			if err != nil {
				return err
			}
			inputID, err := remap(pair.Input.BlockID)
			if err != nil {
				return err
			}
			pair.Output.BlockID = outputID
			pair.Input.BlockID = inputID
		}
	}
	return storeControlRoleSpec(ctx, tx, targetFlowID, spec)
}

func controlRoleBlockIDs(spec ControlRoleSpec) []int64 {
	ids := append([]int64(nil), spec.Plant.Blocks...)
	ids = append(ids, spec.Controller.Blocks...)
	for _, refs := range [][]NamedChannelRef{
		spec.Plant.ExogenousInputs,
		spec.Plant.ControlInputs,
		spec.Plant.PerformanceOutputs,
		spec.Plant.MeasurementOutputs,
		spec.Controller.ReferenceInputs,
		spec.Controller.MeasurementInputs,
		spec.Controller.ControlOutputs,
	} {
		for _, ref := range refs {
			ids = append(ids, ref.BlockID)
		}
	}
	for _, point := range spec.AnalysisPoints {
		for _, pair := range point.Pairs {
			ids = append(ids, pair.Output.BlockID, pair.Input.BlockID)
		}
	}
	return ids
}
