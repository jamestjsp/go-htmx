package studio

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

type FlowDocument struct {
	Version int             `json:"version"`
	Blocks  []DocumentBlock `json:"blocks"`
	Wires   []DocumentWire  `json:"wires"`
}

type DocumentBlock struct {
	ID         int64             `json:"id,omitempty"`
	Kind       BlockKind         `json:"kind"`
	Name       string            `json:"name"`
	Position   DocumentPosition  `json:"position"`
	Parameters map[string]string `json:"parameters"`
}

type DocumentPosition struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type DocumentWire struct {
	ID         int64  `json:"id,omitempty"`
	Source     string `json:"source"`
	SourcePort int    `json:"sourcePort"`
	Target     string `json:"target"`
	TargetPort int    `json:"targetPort"`
}

type FlowApplyResult struct {
	Added         []string `json:"added,omitempty"`
	Updated       []string `json:"updated,omitempty"`
	Removed       []string `json:"removed,omitempty"`
	WiresAdded    int      `json:"wiresAdded"`
	WiresRemoved  int      `json:"wiresRemoved"`
	Changed       bool     `json:"changed"`
	DryRun        bool     `json:"dryRun"`
	modelChanged  bool
	layoutChanged bool
}

type documentBlockState struct {
	DocumentBlock
	Block Block
}

type documentWireIdentity struct {
	source     string
	sourcePort int
	target     string
	targetPort int
}

type documentTargetPortIdentity struct {
	target     string
	targetPort int
}

func (s *Studio) DumpFlow(ctx context.Context, flowID int64) (FlowDocument, error) {
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return FlowDocument{}, err
	}
	document := FlowDocument{Version: 1, Blocks: make([]DocumentBlock, 0, len(snapshot.Blocks)), Wires: make([]DocumentWire, 0, len(snapshot.Connections))}
	for _, block := range snapshot.Blocks {
		parameters := make(map[string]string)
		for _, field := range block.EditorFields() {
			parameters[field.Name] = field.Value
		}
		document.Blocks = append(document.Blocks, DocumentBlock{
			ID: block.ID, Kind: block.Kind, Name: block.Name,
			Position: DocumentPosition{X: block.Position.X, Y: block.Position.Y}, Parameters: parameters,
		})
	}
	names := make(map[int64]string, len(snapshot.Blocks))
	for _, block := range snapshot.Blocks {
		names[block.ID] = block.Name
	}
	for _, connection := range snapshot.Connections {
		document.Wires = append(document.Wires, DocumentWire{
			ID: connection.ID, Source: names[connection.SourceID], SourcePort: connection.SourcePort,
			Target: names[connection.TargetID], TargetPort: connection.TargetPort,
		})
	}
	return document, nil
}

func (s *Studio) ApplyFlow(ctx context.Context, flowID int64, document FlowDocument, dryRun bool) (FlowApplyResult, Snapshot, error) {
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return FlowApplyResult{}, Snapshot{}, err
	}
	states, byName, err := prepareDocument(snapshot, document)
	if err != nil {
		return FlowApplyResult{}, Snapshot{}, err
	}
	result := planDocument(snapshot, states, byName, document)
	result.DryRun = dryRun
	if dryRun || !result.Changed {
		return result, snapshot, nil
	}

	err = s.inTx(ctx, func(tx *sql.Tx) error {
		return s.applyDocumentTx(ctx, tx, flowID, snapshot, states, document, result)
	})
	if err != nil {
		return FlowApplyResult{}, Snapshot{}, err
	}
	updated, err := s.snapshot(ctx, flowID)
	if err != nil {
		return FlowApplyResult{}, Snapshot{}, err
	}
	return result, updated, nil
}

func prepareDocument(snapshot Snapshot, document FlowDocument) ([]documentBlockState, map[string]documentBlockState, error) {
	if document.Version != 0 && document.Version != 1 {
		return nil, nil, invalid("unsupported flowsheet document version %d", document.Version)
	}
	current := make(map[string]Block, len(snapshot.Blocks))
	for _, block := range snapshot.Blocks {
		if _, exists := current[block.Name]; exists {
			return nil, nil, invalid("flowsheet contains duplicate block name %q", block.Name)
		}
		current[block.Name] = block
	}
	states := make([]documentBlockState, 0, len(document.Blocks))
	byName := make(map[string]documentBlockState, len(document.Blocks))
	for _, desired := range document.Blocks {
		if desired.Name == "" {
			return nil, nil, invalid("block name is required")
		}
		if _, exists := byName[desired.Name]; exists {
			return nil, nil, invalid("flowsheet document contains duplicate block name %q", desired.Name)
		}
		if !desired.Kind.Valid() {
			return nil, nil, invalid("unknown block type %q", desired.Kind)
		}
		base, exists := current[desired.Name]
		if exists && base.Kind != desired.Kind {
			return nil, nil, invalid("block %q cannot change kind from %s to %s", desired.Name, base.Kind, desired.Kind)
		}
		if !exists {
			base = Block{Kind: desired.Kind, Name: desired.Name, Parameters: defaultParameters(desired.Kind)}
		}
		validated, err := validateBlockUpdate(base, BlockUpdate{Name: desired.Name, Parameters: desired.Parameters})
		if err != nil {
			return nil, nil, err
		}
		state := documentBlockState{
			DocumentBlock: desired,
			Block:         validated,
		}
		state.Block.Position = Point{X: desired.Position.X, Y: desired.Position.Y}
		states = append(states, state)
		byName[desired.Name] = state
	}
	if err := validateDocumentWires(document.Wires, byName); err != nil {
		return nil, nil, err
	}
	return states, byName, nil
}

func validateDocumentWires(wires []DocumentWire, blocks map[string]documentBlockState) error {
	seen := make(map[documentWireIdentity]struct{}, len(wires))
	occupied := make(map[documentTargetPortIdentity]struct{}, len(wires))
	for _, wire := range wires {
		source, sourceOK := blocks[wire.Source]
		target, targetOK := blocks[wire.Target]
		if !sourceOK || !targetOK {
			return invalid("wire endpoints must name blocks in the document")
		}
		if wire.Source == wire.Target || (source.Block.ID != 0 && source.Block.ID == target.Block.ID) {
			return invalid("a block cannot connect to itself")
		}
		if wire.SourcePort < 0 || !source.Block.hasOutputPort(wire.SourcePort) {
			return invalid("%s has no output port %d", source.Block.Name, wire.SourcePort)
		}
		if wire.TargetPort < 0 || !target.Block.hasInputPort(wire.TargetPort) {
			return invalid("%s has no input port %d", target.Block.Name, wire.TargetPort)
		}
		if err := validateConnectionWidth(source.Block, wire.SourcePort, target.Block, wire.TargetPort); err != nil {
			return err
		}
		wireKey := documentWireIdentity{
			source: wire.Source, sourcePort: wire.SourcePort,
			target: wire.Target, targetPort: wire.TargetPort,
		}
		if _, exists := seen[wireKey]; exists {
			return invalid("those blocks are already connected")
		}
		seen[wireKey] = struct{}{}
		portKey := documentTargetPortIdentity{target: wire.Target, targetPort: wire.TargetPort}
		if _, exists := occupied[portKey]; exists {
			if target.Block.InputPortCount() == 1 {
				return invalid("%s already has an input", target.Block.Name)
			}
			return invalid("%s already has an input on port %d", target.Block.Name, wire.TargetPort)
		}
		occupied[portKey] = struct{}{}
	}
	return nil
}

func planDocument(snapshot Snapshot, states []documentBlockState, byName map[string]documentBlockState, document FlowDocument) FlowApplyResult {
	result := FlowApplyResult{}
	current := make(map[string]Block, len(snapshot.Blocks))
	for _, block := range snapshot.Blocks {
		current[block.Name] = block
	}
	for _, state := range states {
		previous, exists := current[state.Name]
		if !exists {
			result.Added = append(result.Added, state.Name)
			result.modelChanged = true
			continue
		}
		blockChanged := previous.Kind != state.Block.Kind || previous.Name != state.Block.Name || !blockParametersEqual(previous, state.Block)
		positionChanged := previous.Position != state.Block.Position
		if blockChanged || positionChanged {
			result.Updated = append(result.Updated, state.Name)
		}
		result.modelChanged = result.modelChanged || blockChanged
		result.layoutChanged = result.layoutChanged || positionChanged
	}
	for name := range current {
		if _, exists := byName[name]; !exists {
			result.Removed = append(result.Removed, name)
			result.modelChanged = true
		}
	}
	sort.Strings(result.Added)
	sort.Strings(result.Updated)
	sort.Strings(result.Removed)
	currentWires := make(map[documentWireIdentity]struct{}, len(snapshot.Connections))
	names := make(map[int64]string, len(snapshot.Blocks))
	for _, block := range snapshot.Blocks {
		names[block.ID] = block.Name
	}
	for _, wire := range snapshot.Connections {
		currentWires[documentWireIdentity{
			source: names[wire.SourceID], sourcePort: wire.SourcePort,
			target: names[wire.TargetID], targetPort: wire.TargetPort,
		}] = struct{}{}
	}
	desiredWires := make(map[documentWireIdentity]struct{}, len(document.Wires))
	for _, wire := range document.Wires {
		desiredWires[documentWireIdentity{
			source: wire.Source, sourcePort: wire.SourcePort,
			target: wire.Target, targetPort: wire.TargetPort,
		}] = struct{}{}
	}
	for key := range desiredWires {
		if _, exists := currentWires[key]; !exists {
			result.WiresAdded++
		}
	}
	for key := range currentWires {
		if _, exists := desiredWires[key]; !exists {
			result.WiresRemoved++
		}
	}
	result.modelChanged = result.modelChanged || result.WiresAdded != 0 || result.WiresRemoved != 0
	result.Changed = result.modelChanged || result.layoutChanged
	return result
}

func (s *Studio) applyDocumentTx(ctx context.Context, tx *sql.Tx, flowID int64, snapshot Snapshot, states []documentBlockState, document FlowDocument, result FlowApplyResult) error {
	if result.WiresAdded != 0 || result.WiresRemoved != 0 || len(result.Added) != 0 || len(result.Removed) != 0 {
		if _, err := tx.ExecContext(ctx, "DELETE FROM connections WHERE flow_id = ?", flowID); err != nil {
			return fmt.Errorf("replace flowsheet connections: %w", err)
		}
	}
	current := make(map[string]Block, len(snapshot.Blocks))
	ids := make(map[string]int64, len(snapshot.Blocks))
	for _, block := range snapshot.Blocks {
		current[block.Name] = block
		ids[block.Name] = block.ID
	}
	removeIDs := make([]int64, 0, len(result.Removed))
	for _, name := range result.Removed {
		removeIDs = append(removeIDs, current[name].ID)
	}
	if len(removeIDs) != 0 {
		if err := clearControlRoleSpecForBlocks(ctx, tx, flowID, removeIDs); err != nil {
			return err
		}
		for _, blockID := range removeIDs {
			if _, err := tx.ExecContext(ctx, "DELETE FROM blocks WHERE id = ? AND flow_id = ?", blockID, flowID); err != nil {
				return fmt.Errorf("remove document block: %w", err)
			}
		}
	}
	for _, state := range states {
		previous, exists := current[state.Name]
		if !exists {
			encoded, err := encodeParameters(state.Block.Parameters)
			if err != nil {
				return err
			}
			insertResult, err := tx.ExecContext(ctx, `
				INSERT INTO blocks(flow_id, kind, name, x, y, parameters_json)
				VALUES(?, ?, ?, ?, ?, ?)`,
				flowID, state.Block.Kind, state.Block.Name, state.Block.Position.X, state.Block.Position.Y, encoded,
			)
			if err != nil {
				return fmt.Errorf("add document block: %w", err)
			}
			newID, err := insertResult.LastInsertId()
			if err != nil {
				return fmt.Errorf("read document block id: %w", err)
			}
			ids[state.Name] = newID
			continue
		}
		parameters := state.Block.Parameters
		if blockParametersEqual(previous, state.Block) {
			parameters = previous.Parameters
		}
		encoded, err := encodeParameters(parameters)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE blocks SET x = ?, y = ?, parameters_json = ?
			WHERE id = ? AND flow_id = ?`,
			state.Block.Position.X, state.Block.Position.Y, encoded, previous.ID, flowID,
		); err != nil {
			return fmt.Errorf("update document block: %w", err)
		}
	}
	if result.WiresAdded != 0 || result.WiresRemoved != 0 || len(result.Added) != 0 || len(result.Removed) != 0 {
		for _, wire := range document.Wires {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO connections(flow_id, source_id, source_port, target_id, target_port)
				VALUES(?, ?, ?, ?, ?)`, flowID, ids[wire.Source], wire.SourcePort, ids[wire.Target], wire.TargetPort); err != nil {
				return fmt.Errorf("add document connection: %w", err)
			}
		}
	}
	if result.modelChanged {
		return s.touchModel(ctx, tx, flowID, "Applied flowsheet document")
	}
	if result.layoutChanged {
		return s.touchLayout(ctx, tx, flowID)
	}
	return nil
}

func blockParametersEqual(left, right Block) bool {
	if left.Kind != right.Kind {
		return false
	}
	leftFields := make(map[string]string)
	for _, field := range left.EditorFields() {
		leftFields[field.Name] = field.Value
	}
	rightFields := make(map[string]string)
	for _, field := range right.EditorFields() {
		rightFields[field.Name] = field.Value
	}
	if len(leftFields) != len(rightFields) {
		return false
	}
	for name, value := range leftFields {
		if rightFields[name] != value {
			return false
		}
	}
	return true
}
