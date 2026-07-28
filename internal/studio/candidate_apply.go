package studio

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

type ControlRoleSnapshot struct {
	Spec        ControlRoleSpec `json:"spec"`
	Fingerprint string          `json:"fingerprint"`
}

type candidateBlockEdit struct {
	blockID      int64
	expectedKind BlockKind
	parameters   Parameters
}

type candidateApplyRequest struct {
	flowID        int64
	modelRevision time.Time
	controlRoles  ControlRoleSnapshot
	edit          *candidateBlockEdit
	event         string
}

type ControllerUndoCandidate struct {
	FlowID              int64               `json:"flowId"`
	SourceModelRevision time.Time           `json:"sourceModelRevision"`
	SourceControlRoles  ControlRoleSnapshot `json:"sourceControlRoles"`
	Label               string              `json:"label"`
	edit                *candidateBlockEdit
}

type ControllerCandidateApplication struct {
	Snapshot Snapshot                `json:"snapshot"`
	Undo     ControllerUndoCandidate `json:"undo"`
}

// Clone returns an independent undo snapshot suitable for retention across
// requests.
func (candidate ControllerUndoCandidate) Clone() ControllerUndoCandidate {
	cloned := candidate
	cloned.SourceControlRoles.Spec = cloneControlRoleSpec(
		candidate.SourceControlRoles.Spec,
	)
	if candidate.edit != nil {
		edit := *candidate.edit
		edit.parameters = cloneParameters(candidate.edit.parameters)
		cloned.edit = &edit
	}
	return cloned
}

func newControlRoleSnapshot(spec ControlRoleSpec) ControlRoleSnapshot {
	normalized := normalizedControlRoleFingerprintSpec(spec)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		panic(fmt.Sprintf("fingerprint control roles: %v", err))
	}
	sum := sha256.Sum256(encoded)
	return ControlRoleSnapshot{
		Spec:        normalized,
		Fingerprint: hex.EncodeToString(sum[:]),
	}
}

func normalizedControlRoleFingerprintSpec(spec ControlRoleSpec) ControlRoleSpec {
	spec = normalizeControlRoleSpec(spec)
	if spec.Controller.FeedbackConvention == "" {
		spec.Controller.FeedbackConvention = FeedbackExternalNegative
	}
	sort.Slice(spec.Plant.Blocks, func(i, j int) bool {
		return spec.Plant.Blocks[i] < spec.Plant.Blocks[j]
	})
	sort.Slice(spec.Controller.Blocks, func(i, j int) bool {
		return spec.Controller.Blocks[i] < spec.Controller.Blocks[j]
	})
	normalizeEmptyControlRoleSlices(&spec)
	return spec
}

func normalizeEmptyControlRoleSlices(spec *ControlRoleSpec) {
	if len(spec.Plant.Blocks) == 0 {
		spec.Plant.Blocks = nil
	}
	if len(spec.Plant.ExogenousInputs) == 0 {
		spec.Plant.ExogenousInputs = nil
	}
	if len(spec.Plant.ControlInputs) == 0 {
		spec.Plant.ControlInputs = nil
	}
	if len(spec.Plant.PerformanceOutputs) == 0 {
		spec.Plant.PerformanceOutputs = nil
	}
	if len(spec.Plant.MeasurementOutputs) == 0 {
		spec.Plant.MeasurementOutputs = nil
	}
	if len(spec.Controller.Blocks) == 0 {
		spec.Controller.Blocks = nil
	}
	if len(spec.Controller.ReferenceInputs) == 0 {
		spec.Controller.ReferenceInputs = nil
	}
	if len(spec.Controller.MeasurementInputs) == 0 {
		spec.Controller.MeasurementInputs = nil
	}
	if len(spec.Controller.ControlOutputs) == 0 {
		spec.Controller.ControlOutputs = nil
	}
	if len(spec.AnalysisPoints) == 0 {
		spec.AnalysisPoints = nil
	}
	for i := range spec.AnalysisPoints {
		if len(spec.AnalysisPoints[i].Pairs) == 0 {
			spec.AnalysisPoints[i].Pairs = nil
		}
	}
}

func (snapshot ControlRoleSnapshot) valid() bool {
	if snapshot.Fingerprint == "" {
		return false
	}
	return newControlRoleSnapshot(snapshot.Spec).Fingerprint == snapshot.Fingerprint
}

func editBlockWithTunedChanges(
	block Block,
	changes []tunedParameterChange,
) (*candidateBlockEdit, error) {
	if len(changes) == 0 {
		return nil, invalid("controller candidate has no parameter changes")
	}
	parameters := cloneParameters(block.Parameters)
	for _, change := range changes {
		if change.ref.BlockID != block.ID {
			return nil, invalid(
				"controller candidate references block %d outside its authored controller",
				change.ref.BlockID,
			)
		}
		if err := setTunedParameter(&parameters, change.ref, change.value); err != nil {
			return nil, err
		}
	}
	if err := validateParameters(block.Kind, parameters); err != nil {
		return nil, invalid("%s: %s", block.Name, err)
	}
	return &candidateBlockEdit{
		blockID: block.ID, expectedKind: block.Kind, parameters: parameters,
	}, nil
}

func (s *Studio) applyCandidateBlockEdit(
	ctx context.Context,
	request candidateApplyRequest,
) (Snapshot, error) {
	result, err := s.applyCandidateBlockEditWithUndo(ctx, request)
	return result.Snapshot, err
}

func (s *Studio) applyCandidateBlockEditWithUndo(
	ctx context.Context,
	request candidateApplyRequest,
) (ControllerCandidateApplication, error) {
	if request.flowID <= 0 || request.modelRevision.IsZero() ||
		!request.controlRoles.valid() || request.edit == nil ||
		request.edit.blockID <= 0 || request.event == "" {
		return ControllerCandidateApplication{}, invalid(
			"controller candidate is incomplete; refresh the design",
		)
	}
	var previous *candidateBlockEdit
	var appliedRevision time.Time
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var revision string
		if err := tx.QueryRowContext(ctx,
			"SELECT model_updated_at FROM flows WHERE id = ?", request.flowID,
		).Scan(&revision); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if revision != request.modelRevision.UTC().Format(time.RFC3339Nano) {
			return invalid("controller candidate is stale; refresh the design from the current model")
		}
		currentSpec, err := loadControlRoleSpec(ctx, tx, request.flowID)
		if err != nil {
			return err
		}
		if newControlRoleSnapshot(currentSpec).Fingerprint !=
			request.controlRoles.Fingerprint {
			return invalid("control roles changed; refresh the design from the current roles")
		}

		edit := request.edit
		block, err := blockByID(ctx, tx, edit.blockID)
		if err != nil {
			return err
		}
		if block.FlowID != request.flowID || block.Kind != edit.expectedKind {
			return invalid("controller target block changed; refresh the design")
		}
		previous = &candidateBlockEdit{
			blockID: block.ID, expectedKind: block.Kind,
			parameters: cloneParameters(block.Parameters),
		}
		parameters := cloneParameters(edit.parameters)
		if err := validateParameters(block.Kind, parameters); err != nil {
			return invalid("%s: %s", block.Name, err)
		}
		block.Parameters = parameters
		if err := checkWiredInputPorts(ctx, tx, block); err != nil {
			return err
		}
		if err := checkWiredPortCompatibility(ctx, tx, block); err != nil {
			return err
		}
		encoded, err := encodeParameters(parameters)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE blocks SET parameters_json = ? WHERE id = ?",
			encoded, block.ID,
		); err != nil {
			return fmt.Errorf("apply controller candidate: %w", err)
		}
		if err := s.touchModel(ctx, tx, request.flowID, request.event); err != nil {
			return err
		}
		var applied string
		if err := tx.QueryRowContext(ctx,
			"SELECT model_updated_at FROM flows WHERE id = ?", request.flowID,
		).Scan(&applied); err != nil {
			return err
		}
		appliedRevision, err = time.Parse(time.RFC3339Nano, applied)
		return err
	})
	if err != nil {
		return ControllerCandidateApplication{}, err
	}
	snapshot, err := s.snapshot(ctx, request.flowID)
	if err != nil {
		return ControllerCandidateApplication{}, err
	}
	return ControllerCandidateApplication{
		Snapshot: snapshot,
		Undo: ControllerUndoCandidate{
			FlowID:              request.flowID,
			SourceModelRevision: appliedRevision,
			SourceControlRoles:  request.controlRoles,
			Label:               request.event,
			edit:                previous,
		},
	}, nil
}

func (s *Studio) UndoControllerCandidate(
	ctx context.Context,
	candidate ControllerUndoCandidate,
) (Snapshot, error) {
	if candidate.Label == "" {
		return Snapshot{}, invalid(
			"controller undo candidate is incomplete; apply a fresh design",
		)
	}
	return s.applyCandidateBlockEdit(ctx, candidateApplyRequest{
		flowID:        candidate.FlowID,
		modelRevision: candidate.SourceModelRevision,
		controlRoles:  candidate.SourceControlRoles,
		edit:          candidate.edit,
		event:         "Undid " + candidate.Label,
	})
}
