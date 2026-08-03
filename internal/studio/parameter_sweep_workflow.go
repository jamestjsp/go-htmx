package studio

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jamestjsp/controlsys"
)

// RunParameterSweep builds the sweep source from a block in the requested
// flowsheet. The snapshot owns both the authored parameters and the model
// revision, so callers cannot provide closures or claim a revision they did
// not read.
func (s *Studio) RunParameterSweep(
	ctx context.Context,
	flowID int64,
	blockID int64,
	spec SweepSpec,
	analysisSpec SweepAnalysisSpec,
) (*ParameterSweepAnalysis, error) {
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return nil, err
	}
	var target Block
	found := false
	for _, block := range snapshot.Blocks {
		if block.ID == blockID {
			target = block
			found = true
			break
		}
	}
	if !found {
		return nil, ErrNotFound
	}
	definition, ok := blockDefinitions[target.Kind]
	if !ok {
		return nil, invalid("unknown block type %q", target.Kind)
	}
	setters := make(map[string]parameterDefinition, len(definition.Parameters))
	for _, field := range definition.Parameters {
		setters[field.Name] = field
	}
	for _, axis := range spec.Axes {
		if _, ok := setters[axis.Parameter]; !ok {
			return nil, invalid(
				"parameter sweep axis %s is not defined for block %s",
				axis.Parameter, target.Name,
			)
		}
	}

	baseBlocks := make([]Block, len(snapshot.Blocks))
	for index, block := range snapshot.Blocks {
		baseBlocks[index] = block
		baseBlocks[index].Parameters = cloneParameters(block.Parameters)
	}
	source := SweepModelSource{
		Name:          target.Name,
		ModelRevision: snapshot.Flow.ModelUpdatedAt,
		Parameters:    cloneParameters(target.Parameters),
		SetParameter: func(parameters *Parameters, name string, value float64) error {
			field, ok := setters[name]
			if !ok {
				return fmt.Errorf("parameter %q is not defined for block %s", name, target.Name)
			}
			if err := field.set(parameters, strconv.FormatFloat(value, 'g', -1, 64)); err != nil {
				return err
			}
			return validateParameters(target.Kind, *parameters)
		},
		Compile: func(name string, parameters Parameters) (*controlsys.System, error) {
			blocks := make([]Block, len(baseBlocks))
			for index, block := range baseBlocks {
				blocks[index] = block
				blocks[index].Parameters = cloneParameters(block.Parameters)
				if block.ID == target.ID {
					blocks[index].Name = name
					blocks[index].Parameters = cloneParameters(parameters)
				}
			}
			compiled, err := compileModel(blocks, snapshot.Connections)
			if err != nil {
				return nil, err
			}
			system := compiled.systemCopy()
			states, _, _ := system.Dims()
			if len(system.StateName) != states {
				system.StateName = make([]string, states)
				for index := range states {
					system.StateName[index] = fmt.Sprintf("state_%d", index+1)
				}
			}
			return system, nil
		},
	}
	spec.SourceModelRevision = snapshot.Flow.ModelUpdatedAt
	sweep, err := MaterializeParameterSweep(source, spec)
	if err != nil {
		return nil, err
	}
	return AnalyzeParameterSweep(sweep, analysisSpec)
}
