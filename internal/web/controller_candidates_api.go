package web

import (
	"errors"
	"net/http"

	"github.com/jamestjsp/process-lab/internal/studio"
)

type pidCandidateAPIRequest struct {
	studio.PIDDesignRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

type stateCandidateAPIRequest struct {
	studio.ObserverRegulatorRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

type robustCandidateAPIRequest struct {
	studio.RobustSynthesisRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

type stateFeedbackCandidateAPIRequest struct {
	studio.StateFeedbackRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

type estimatorCandidateAPIRequest struct {
	studio.EstimatorDesignRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

type tuningCandidateAPIRequest struct {
	studio.ControllerTuningRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

type controllerCandidateAPIRecord struct {
	ID            string                           `json:"id"`
	FlowID        int64                            `json:"flowId"`
	Kind          string                           `json:"kind"`
	Review        studio.ControllerCandidateReview `json:"review"`
	Applied       bool                             `json:"applied"`
	UndoAvailable bool                             `json:"undoAvailable"`
}

type controllerCandidateActionAPIRecord struct {
	ID     string `json:"id"`
	FlowID int64  `json:"flowId"`
	Kind   string `json:"kind"`
	Action string `json:"action"`
}

func controllerCandidateRecord(candidate *pendingControllerCandidate) controllerCandidateAPIRecord {
	return controllerCandidateAPIRecord{
		ID: candidate.ID, FlowID: candidate.FlowID, Kind: candidate.Kind,
		Review: candidate.Review, Applied: candidate.Applied,
		UndoAvailable: candidate.Undo != nil,
	}
}

func (s *Server) controllerCandidateShowAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	candidate := s.controllerCandidates.get(r.PathValue("candidateID"), flowID)
	if candidate == nil {
		return apiResponse{}, studio.ErrNotFound
	}
	return apiResponse{Value: controllerCandidateRecord(candidate)}, nil
}

func (s *Server) controllerCandidatePIDAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input pidCandidateAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	candidate, err := s.studio.DesignPIDController(r.Context(), flowID, input.PIDDesignRequest)
	if err != nil {
		return apiResponse{}, err
	}
	review, err := s.studio.ReviewPIDDesignCandidate(r.Context(), candidate, input.ReviewHorizon)
	if err != nil {
		return apiResponse{}, err
	}
	return s.storeControllerCandidate(
		flowID, "pid", review, &candidate, nil, nil, nil,
	)
}

func (s *Server) controllerCandidateStateAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input stateCandidateAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	candidate, err := s.studio.DesignObserverRegulator(r.Context(), flowID, input.ObserverRegulatorRequest)
	if err != nil {
		return apiResponse{}, err
	}
	review, err := s.studio.ReviewStateDesignCandidate(r.Context(), candidate, input.ReviewHorizon)
	if err != nil {
		return apiResponse{}, err
	}
	return s.storeControllerCandidate(
		flowID, "state-space", review, nil, &candidate, nil, nil,
	)
}

func (s *Server) controllerCandidateRobustAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input robustCandidateAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	candidate, err := s.studio.DesignRobustController(r.Context(), flowID, input.RobustSynthesisRequest)
	if err != nil {
		return apiResponse{}, err
	}
	review, err := s.studio.ReviewRobustSynthesisCandidate(r.Context(), candidate, input.ReviewHorizon)
	if err != nil {
		return apiResponse{}, err
	}
	return s.storeControllerCandidate(
		flowID, "robust-synthesis", review, nil, nil, &candidate, nil,
	)
}

func (s *Server) storeControllerCandidate(
	flowID int64,
	kind string,
	review studio.ControllerCandidateReview,
	pid *studio.PIDDesignCandidate,
	state *studio.StateDesignCandidate,
	robust *studio.RobustSynthesisCandidate,
	tuning *studio.ControllerTuningCandidate,
) (apiResponse, error) {
	id, err := newControllerCandidateID()
	if err != nil {
		return apiResponse{}, err
	}
	candidate := &pendingControllerCandidate{
		ID: id, FlowID: flowID, Kind: kind, Review: review,
		PID: pid, State: state, Robust: robust, Tuning: tuning,
	}
	if conflict := s.controllerCandidates.put(candidate); conflict != controllerCandidatePutOK {
		return apiResponse{}, apiConflict(conflict.message())
	}
	return apiResponse{
		Status: http.StatusCreated,
		Value:  controllerCandidateRecord(candidate),
	}, nil
}

func (s *Server) controllerCandidateStateFeedbackAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input stateFeedbackCandidateAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	candidate, err := s.studio.DesignStateFeedback(r.Context(), flowID, input.StateFeedbackRequest)
	if err != nil {
		return apiResponse{}, err
	}
	review, err := s.studio.ReviewStateDesignCandidate(r.Context(), candidate, input.ReviewHorizon)
	if err != nil {
		return apiResponse{}, err
	}
	return s.storeControllerCandidate(flowID, "state-space", review, nil, &candidate, nil, nil)
}

func (s *Server) controllerCandidateEstimatorAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input estimatorCandidateAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	candidate, err := s.studio.DesignEstimator(r.Context(), flowID, input.EstimatorDesignRequest)
	if err != nil {
		return apiResponse{}, err
	}
	review, err := s.studio.ReviewEstimatorCandidate(r.Context(), candidate)
	if err != nil {
		return apiResponse{}, err
	}
	return s.storeControllerCandidate(flowID, "state-estimator", review, nil, &candidate, nil, nil)
}

func (s *Server) controllerCandidateTuningAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input tuningCandidateAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	candidate, err := s.studio.TuneController(r.Context(), flowID, input.ControllerTuningRequest)
	if err != nil {
		return apiResponse{}, err
	}
	review, err := s.studio.ReviewTuningCandidate(r.Context(), candidate, input.ReviewHorizon)
	if err != nil {
		return apiResponse{}, err
	}
	return s.storeControllerCandidate(flowID, "tuning", review, nil, nil, nil, &candidate)
}

func (s *Server) controllerCandidateApplyAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	id := r.PathValue("candidateID")
	if known := s.controllerCandidates.get(id, flowID); known != nil {
		if known.Applied {
			return apiResponse{}, apiConflict("Controller candidate is already applied; undo it before applying another candidate.")
		}
		if known.applying || known.undoing {
			return apiResponse{}, apiConflict("Another controller candidate action is in progress. Try again after it finishes.")
		}
	}
	candidate, release := s.controllerCandidates.beginApply(id, flowID)
	if candidate == nil {
		return apiResponse{}, studio.ErrNotFound
	}
	defer release()
	var application studio.ControllerCandidateApplication
	switch candidate.Kind {
	case "pid":
		application, err = s.studio.ApplyPIDDesignCandidate(r.Context(), *candidate.PID)
	case "state-space":
		application, err = s.studio.ApplyStateDesignCandidate(r.Context(), *candidate.State)
	case "robust-synthesis":
		application, err = s.studio.ApplyRobustSynthesisCandidate(r.Context(), *candidate.Robust)
	case "tuning":
		application, err = s.studio.ApplyTuningCandidate(r.Context(), *candidate.Tuning)
	case "state-estimator":
		err = errors.New("estimator candidates are diagnostic-only and cannot be applied")
	default:
		err = errors.New("unsupported controller candidate kind")
	}
	if err != nil {
		if s.controllerCandidates.finishApply(id, nil) == nil {
			return apiResponse{}, studio.ErrNotFound
		}
		return apiResponse{}, err
	}
	if s.controllerCandidates.finishApply(id, &application.Undo) == nil {
		return apiResponse{}, apiConflict("Process Lab applied the candidate but could not retain its undo state.")
	}
	return apiResponse{Value: controllerCandidateActionAPIRecord{
		ID: id, FlowID: flowID, Kind: candidate.Kind, Action: "applied",
	}}, nil
}

func (s *Server) controllerCandidateUndoAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	id := r.PathValue("candidateID")
	if known := s.controllerCandidates.get(id, flowID); known != nil {
		if !known.Applied || known.Undo == nil {
			return apiResponse{}, apiConflict("Undo is unavailable because this controller candidate has not been applied.")
		}
		if known.applying || known.undoing {
			return apiResponse{}, apiConflict("Another controller candidate action is in progress. Try again after it finishes.")
		}
	}
	candidate, release := s.controllerCandidates.beginUndo(id, flowID)
	if candidate == nil {
		return apiResponse{}, studio.ErrNotFound
	}
	defer release()
	if _, err := s.studio.UndoControllerCandidate(r.Context(), *candidate.Undo); err != nil {
		var validation *studio.ValidationError
		if errors.As(err, &validation) || errors.Is(err, studio.ErrNotFound) {
			s.controllerCandidates.finishUndo(id, true)
		} else {
			s.controllerCandidates.finishUndo(id, false)
		}
		return apiResponse{}, err
	}
	s.controllerCandidates.finishUndo(id, true)
	return apiResponse{Value: controllerCandidateActionAPIRecord{
		ID: id, FlowID: flowID, Kind: candidate.Kind, Action: "undone",
	}}, nil
}
