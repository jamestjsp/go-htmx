package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/jamestjsp/controlsys"
	"github.com/jamestjsp/process-lab/internal/studio"
)

const maxPendingControllerCandidates = 32

type controllerCandidatePutConflict uint8

const (
	controllerCandidatePutOK controllerCandidatePutConflict = iota
	controllerCandidatePutBusy
	controllerCandidatePutUndoAvailable
	controllerCandidatePutCapacity
)

func (conflict controllerCandidatePutConflict) message() string {
	switch conflict {
	case controllerCandidatePutBusy:
		return "Another controller candidate action is in progress. Try again after it finishes."
	case controllerCandidatePutUndoAvailable:
		return "Undo or change the model before generating another controller candidate."
	default:
		return "The controller candidate workspace is full. Try again after another candidate finishes."
	}
}

type pendingControllerCandidate struct {
	ID       string
	FlowID   int64
	Kind     string
	Review   studio.ControllerCandidateReview
	PID      *studio.PIDDesignCandidate
	State    *studio.StateDesignCandidate
	Robust   *studio.RobustSynthesisCandidate
	Undo     *studio.ControllerUndoCandidate
	Applied  bool
	applying bool
	undoing  bool
}

type controllerCandidateRegistry struct {
	mu     sync.Mutex
	byID   map[string]*pendingControllerCandidate
	byFlow map[int64]string
	order  []string
}

func newControllerCandidateRegistry() *controllerCandidateRegistry {
	return &controllerCandidateRegistry{
		byID:   make(map[string]*pendingControllerCandidate),
		byFlow: make(map[int64]string),
	}
}

func (registry *controllerCandidateRegistry) put(
	candidate *pendingControllerCandidate,
) controllerCandidatePutConflict {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if previous := registry.byFlow[candidate.FlowID]; previous != "" {
		current := registry.byID[previous]
		if current != nil && (current.applying || current.undoing) {
			return controllerCandidatePutBusy
		}
		if current != nil && current.Applied && current.Undo != nil &&
			candidate.Review.SourceModelRevision.Equal(
				current.Undo.SourceModelRevision,
			) &&
			candidate.Review.SourceControlRoles.Fingerprint ==
				current.Undo.SourceControlRoles.Fingerprint {
			return controllerCandidatePutUndoAvailable
		}
		registry.deleteLocked(previous)
	}
	registry.byID[candidate.ID] = clonePendingControllerCandidate(candidate)
	registry.byFlow[candidate.FlowID] = candidate.ID
	registry.order = append(registry.order, candidate.ID)
	scanned := 0
	for len(registry.byID) > maxPendingControllerCandidates &&
		len(registry.order) > 0 && scanned < len(registry.order) {
		oldest := registry.order[0]
		registry.order = registry.order[1:]
		old := registry.byID[oldest]
		if old == nil {
			continue
		}
		if old.applying || old.undoing {
			registry.order = append(registry.order, oldest)
			scanned++
			continue
		}
		delete(registry.byID, oldest)
		if registry.byFlow[old.FlowID] == oldest {
			delete(registry.byFlow, old.FlowID)
		}
	}
	if registry.byID[candidate.ID] == nil {
		return controllerCandidatePutCapacity
	}
	return controllerCandidatePutOK
}

func (registry *controllerCandidateRegistry) forFlow(
	flowID int64,
) *pendingControllerCandidate {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return clonePendingControllerCandidate(
		registry.byID[registry.byFlow[flowID]],
	)
}

func (registry *controllerCandidateRegistry) beginApply(
	id string,
	flowID int64,
) (*pendingControllerCandidate, func()) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	candidate := registry.byID[id]
	if candidate == nil || candidate.FlowID != flowID || candidate.Applied ||
		candidate.applying || candidate.undoing {
		return nil, func() {}
	}
	candidate.applying = true
	return clonePendingControllerCandidate(candidate), func() {
		registry.finishApply(id, nil)
	}
}

func (registry *controllerCandidateRegistry) finishApply(
	id string,
	undo *studio.ControllerUndoCandidate,
) *pendingControllerCandidate {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	candidate := registry.byID[id]
	if candidate == nil || !candidate.applying {
		return nil
	}
	candidate.applying = false
	if undo != nil {
		stored := undo.Clone()
		candidate.Undo = &stored
		candidate.Applied = true
	}
	return clonePendingControllerCandidate(candidate)
}

func (registry *controllerCandidateRegistry) beginUndo(
	id string,
	flowID int64,
) (*pendingControllerCandidate, func()) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	candidate := registry.byID[id]
	if candidate == nil || candidate.FlowID != flowID || !candidate.Applied ||
		candidate.Undo == nil || candidate.applying || candidate.undoing {
		return nil, func() {}
	}
	candidate.undoing = true
	return clonePendingControllerCandidate(candidate), func() {
		registry.finishUndo(id, false)
	}
}

func (registry *controllerCandidateRegistry) finishUndo(
	id string,
	applied bool,
) *pendingControllerCandidate {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	candidate := registry.byID[id]
	if candidate == nil || !candidate.undoing {
		return nil
	}
	if !applied {
		candidate.undoing = false
		return clonePendingControllerCandidate(candidate)
	}
	registry.deleteLocked(id)
	if registry.byFlow[candidate.FlowID] == id {
		delete(registry.byFlow, candidate.FlowID)
	}
	return nil
}

func (registry *controllerCandidateRegistry) deleteFlow(flowID int64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	id := registry.byFlow[flowID]
	delete(registry.byFlow, flowID)
	registry.deleteLocked(id)
}

func (registry *controllerCandidateRegistry) deleteLocked(id string) {
	delete(registry.byID, id)
	for index, orderedID := range registry.order {
		if orderedID != id {
			continue
		}
		registry.order = append(registry.order[:index], registry.order[index+1:]...)
		return
	}
}

func clonePendingControllerCandidate(
	candidate *pendingControllerCandidate,
) *pendingControllerCandidate {
	if candidate == nil {
		return nil
	}
	cloned := *candidate
	if candidate.Undo != nil {
		undo := candidate.Undo.Clone()
		cloned.Undo = &undo
	}
	return &cloned
}

func newControllerCandidateID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (s *Server) designPIDCandidate(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderControllerCandidateFailure(w, r, flowID, "Invalid PID design settings.")
		return
	}
	crossover, crossoverErr := optionalFormFloat(r, "crossover_frequency")
	phaseMargin, phaseErr := optionalFormFloat(r, "phase_margin")
	horizon, horizonErr := optionalFormFloat(r, "review_horizon")
	baseStep, baseStepErr := optionalFormFloat(r, "base_step")
	setpointWeight, setpointErr := optionalFloatPointer(r.FormValue("setpoint_weight"))
	derivativeWeight, derivativeErr := optionalFloatPointer(r.FormValue("derivative_weight"))
	if crossoverErr != nil || phaseErr != nil || horizonErr != nil ||
		baseStepErr != nil || setpointErr != nil || derivativeErr != nil {
		s.renderControllerCandidateFailure(
			w, r, flowID, "PID design settings must be finite numbers.",
		)
		return
	}
	candidate, err := s.studio.DesignPIDController(
		r.Context(),
		flowID,
		studio.PIDDesignRequest{
			Type:               controlsys.PidtuneType(r.FormValue("pid_type")),
			CrossoverFrequency: crossover, PhaseMargin: phaseMargin,
			SetpointWeight: setpointWeight, DerivativeWeight: derivativeWeight,
			StepHorizon: horizon, BaseStep: baseStep,
		},
	)
	if err != nil {
		s.renderControllerCandidateFailure(w, r, flowID, studio.ValidationMessage(err))
		return
	}
	review, err := s.studio.ReviewPIDDesignCandidate(
		r.Context(), candidate, horizon,
	)
	if err != nil {
		s.renderControllerCandidateFailure(w, r, flowID, studio.ValidationMessage(err))
		return
	}
	id, err := newControllerCandidateID()
	if err != nil {
		http.Error(w, "Process Lab could not create a candidate.", http.StatusInternalServerError)
		return
	}
	pending := &pendingControllerCandidate{
		ID: id, FlowID: flowID, Kind: "pid", Review: review, PID: &candidate,
	}
	if conflict := s.controllerCandidates.put(pending); conflict != controllerCandidatePutOK {
		s.renderControllerCandidatePutConflict(w, r, flowID, conflict)
		return
	}
	s.renderControllerCandidate(w, r, pending, "")
}

func (s *Server) designStateCandidate(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderControllerCandidateFailure(w, r, flowID, "Invalid state-space design settings.")
		return
	}
	q, err := studio.ParseMatrixValue(r.FormValue("q"))
	if err != nil {
		s.renderControllerCandidateFailure(w, r, flowID, "Q: "+studio.ValidationMessage(err))
		return
	}
	rcost, err := studio.ParseMatrixValue(r.FormValue("r"))
	if err != nil {
		s.renderControllerCandidateFailure(w, r, flowID, "R: "+studio.ValidationMessage(err))
		return
	}
	qn, err := studio.ParseMatrixValue(r.FormValue("qn"))
	if err != nil {
		s.renderControllerCandidateFailure(w, r, flowID, "Qn: "+studio.ValidationMessage(err))
		return
	}
	rn, err := studio.ParseMatrixValue(r.FormValue("rn"))
	if err != nil {
		s.renderControllerCandidateFailure(w, r, flowID, "Rn: "+studio.ValidationMessage(err))
		return
	}
	horizon, horizonErr := optionalFormFloat(r, "review_horizon")
	baseStep, baseStepErr := optionalFormFloat(r, "base_step")
	if horizonErr != nil || baseStepErr != nil {
		s.renderControllerCandidateFailure(
			w, r, flowID, "State-space design settings must be finite numbers.",
		)
		return
	}
	candidate, err := s.studio.DesignObserverRegulator(
		r.Context(),
		flowID,
		studio.ObserverRegulatorRequest{
			Method: studio.ObserverRegulatorLQG,
			Q:      &q, R: &rcost, Qn: &qn, Rn: &rn, BaseStep: baseStep,
		},
	)
	if err != nil {
		s.renderControllerCandidateFailure(w, r, flowID, studio.ValidationMessage(err))
		return
	}
	review, err := s.studio.ReviewStateDesignCandidate(
		r.Context(), candidate, horizon,
	)
	if err != nil {
		s.renderControllerCandidateFailure(w, r, flowID, studio.ValidationMessage(err))
		return
	}
	id, err := newControllerCandidateID()
	if err != nil {
		http.Error(w, "Process Lab could not create a candidate.", http.StatusInternalServerError)
		return
	}
	pending := &pendingControllerCandidate{
		ID: id, FlowID: flowID, Kind: "state-space",
		Review: review, State: &candidate,
	}
	if conflict := s.controllerCandidates.put(pending); conflict != controllerCandidatePutOK {
		s.renderControllerCandidatePutConflict(w, r, flowID, conflict)
		return
	}
	s.renderControllerCandidate(w, r, pending, "")
}

func (s *Server) designRobustCandidate(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderControllerCandidateFailure(
			w, r, flowID, "Invalid robust-synthesis settings.",
		)
		return
	}
	horizon, horizonErr := optionalFormFloat(r, "review_horizon")
	baseStep, baseStepErr := optionalFormFloat(r, "base_step")
	if horizonErr != nil || baseStepErr != nil {
		s.renderControllerCandidateFailure(
			w, r, flowID, "Robust-synthesis settings must be finite numbers.",
		)
		return
	}
	candidate, err := s.studio.DesignRobustController(
		r.Context(),
		flowID,
		studio.RobustSynthesisRequest{
			Method:   studio.RobustSynthesisMethod(r.FormValue("method")),
			BaseStep: baseStep,
		},
	)
	if err != nil {
		s.renderControllerCandidateFailure(
			w, r, flowID, studio.ValidationMessage(err),
		)
		return
	}
	review, err := s.studio.ReviewRobustSynthesisCandidate(
		r.Context(), candidate, horizon,
	)
	if err != nil {
		s.renderControllerCandidateFailure(
			w, r, flowID, studio.ValidationMessage(err),
		)
		return
	}
	id, err := newControllerCandidateID()
	if err != nil {
		http.Error(
			w, "Process Lab could not create a candidate.",
			http.StatusInternalServerError,
		)
		return
	}
	pending := &pendingControllerCandidate{
		ID: id, FlowID: flowID, Kind: "robust-synthesis",
		Review: review, Robust: &candidate,
	}
	if conflict := s.controllerCandidates.put(pending); conflict != controllerCandidatePutOK {
		s.renderControllerCandidatePutConflict(w, r, flowID, conflict)
		return
	}
	s.renderControllerCandidate(w, r, pending, "")
}

func (s *Server) applyControllerCandidate(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	id := r.PathValue("candidateID")
	candidate, release := s.controllerCandidates.beginApply(id, flowID)
	if candidate == nil {
		s.renderControllerCandidateFailure(
			w, r, flowID, "This controller candidate expired or was replaced. Generate a fresh candidate.",
		)
		return
	}
	defer release()
	var (
		result studio.ControllerCandidateApplication
		err    error
	)
	switch candidate.Kind {
	case "pid":
		result, err = s.studio.ApplyPIDDesignCandidateWithUndo(
			r.Context(), *candidate.PID,
		)
	case "state-space":
		result, err = s.studio.ApplyStateDesignCandidateWithUndo(
			r.Context(), *candidate.State,
		)
	case "robust-synthesis":
		result, err = s.studio.ApplyRobustSynthesisCandidateWithUndo(
			r.Context(), *candidate.Robust,
		)
	default:
		err = fmt.Errorf("unsupported controller candidate kind %q", candidate.Kind)
	}
	if err != nil {
		candidate = s.controllerCandidates.finishApply(id, nil)
		if candidate == nil {
			s.renderControllerCandidateFailure(
				w, r, flowID,
				"This controller candidate expired or was replaced. Generate a fresh candidate.",
			)
			return
		}
		s.renderControllerCandidate(w, r, candidate, studio.ValidationMessage(err))
		return
	}
	if s.controllerCandidates.finishApply(id, &result.Undo) == nil {
		http.Error(
			w, "Process Lab applied the candidate but could not retain its undo state.",
			http.StatusConflict,
		)
		return
	}
	s.renderWorkbench(
		w, r, result.Snapshot, selectedID(r),
		"Controller candidate applied. Undo is available until the model changes.",
	)
}

func (s *Server) undoControllerCandidate(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	id := r.PathValue("candidateID")
	candidate, release := s.controllerCandidates.beginUndo(id, flowID)
	if candidate == nil {
		s.renderControllerCandidateFailure(
			w, r, flowID, "This controller undo expired. Generate a fresh candidate.",
		)
		return
	}
	defer release()
	snapshot, err := s.studio.UndoControllerCandidate(
		r.Context(), *candidate.Undo,
	)
	if err != nil {
		var validation *studio.ValidationError
		if errors.As(err, &validation) || errors.Is(err, studio.ErrNotFound) {
			s.controllerCandidates.finishUndo(id, true)
			s.renderControllerCandidateFailure(
				w, r, flowID, studio.ValidationMessage(err),
			)
			return
		}
		candidate = s.controllerCandidates.finishUndo(id, false)
		if candidate == nil {
			s.renderControllerCandidateFailure(
				w, r, flowID,
				"This controller undo expired. Generate a fresh candidate.",
			)
			return
		}
		s.renderControllerCandidate(w, r, candidate, studio.ValidationMessage(err))
		return
	}
	s.controllerCandidates.finishUndo(id, true)
	s.renderWorkbench(w, r, snapshot, selectedID(r), "Controller candidate undone.")
}

func (s *Server) renderControllerCandidatePutConflict(
	w http.ResponseWriter,
	r *http.Request,
	flowID int64,
	conflict controllerCandidatePutConflict,
) {
	if current := s.controllerCandidates.forFlow(flowID); current != nil {
		s.renderControllerCandidate(w, r, current, conflict.message())
		return
	}
	s.renderControllerCandidateFailure(w, r, flowID, conflict.message())
}

func optionalFloatPointer(raw string) (*float64, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (s *Server) renderControllerCandidateFailure(
	w http.ResponseWriter,
	r *http.Request,
	flowID int64,
	message string,
) {
	if r.Header.Get("HX-Request") != "true" {
		s.renderFailure(w, r, flowID, 0, message)
		return
	}
	candidate := s.controllerCandidates.forFlow(flowID)
	if candidate == nil {
		candidate = &pendingControllerCandidate{FlowID: flowID}
	}
	s.renderControllerCandidate(
		w,
		r,
		candidate,
		message,
	)
}

func (s *Server) renderControllerCandidate(
	w http.ResponseWriter,
	r *http.Request,
	candidate *pendingControllerCandidate,
	message string,
) {
	if r.Header.Get("HX-Request") != "true" {
		snapshot, err := s.studio.Snapshot(r.Context(), candidate.FlowID)
		if err != nil {
			http.Error(w, studio.ValidationMessage(err), http.StatusBadRequest)
			return
		}
		s.renderWorkbench(w, r, snapshot, selectedID(r), message)
		return
	}
	w.Header().Set("HX-Retarget", "#controller-candidate")
	w.Header().Set("HX-Reswap", "innerHTML")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	view := newControllerCandidateView(candidate, message)
	if err := s.templates.ExecuteTemplate(w, "controller-candidate", view); err != nil {
		http.Error(
			w, "Process Lab could not render the controller candidate.",
			http.StatusInternalServerError,
		)
	}
}

type controllerCandidateView struct {
	ID              string
	FlowID          int64
	Kind            string
	Algorithm       string
	Revision        string
	RoleFingerprint string
	Goals           []studio.ControllerDesignGoal
	Warnings        []string
	Details         []analysisMetricView
	Comparisons     []controllerComparisonMetricView
	Plots           []analysisPlotView
	ApplyAvailable  bool
	UndoAvailable   bool
	ApplyPath       string
	UndoPath        string
	RefreshFormID   string
	Applied         bool
	Error           string
}

type controllerComparisonMetricView struct {
	Label     string
	Current   string
	Candidate string
}

func (s *Server) newWorkbenchView(
	workspace studio.Workspace,
	selectedID int64,
	errorMessage string,
) workbenchView {
	view := newWorkbenchView(workspace, selectedID, errorMessage)
	if pending := s.controllerCandidates.forFlow(workspace.Snapshot.Flow.ID); pending != nil {
		candidate := newControllerCandidateView(pending, "")
		view.ControllerCandidate = &candidate
	}
	return view
}

func newControllerCandidateView(
	pending *pendingControllerCandidate,
	errorMessage string,
) controllerCandidateView {
	view := controllerCandidateView{
		ID: pending.ID, FlowID: pending.FlowID, Kind: pending.Kind,
		Error: errorMessage, Applied: pending.Applied,
	}
	if pending.ID == "" {
		return view
	}
	review := pending.Review
	view.Algorithm = review.Algorithm
	view.Revision = review.SourceModelRevision.Local().Format("15:04:05.000")
	view.RoleFingerprint = review.SourceControlRoles.Fingerprint
	if len(view.RoleFingerprint) > 12 {
		view.RoleFingerprint = view.RoleFingerprint[:12]
	}
	view.Goals = append([]studio.ControllerDesignGoal(nil), review.Goals...)
	view.Warnings = append([]string(nil), review.Warnings...)
	view.ApplyAvailable = review.ApplyAvailable && !pending.Applied &&
		!pending.applying && !pending.undoing
	view.UndoAvailable = pending.Applied && pending.Undo != nil &&
		!pending.applying && !pending.undoing
	view.ApplyPath = fmt.Sprintf(
		"/flows/%d/controller-candidates/%s/apply", pending.FlowID, pending.ID,
	)
	view.UndoPath = fmt.Sprintf(
		"/flows/%d/controller-candidates/%s/undo", pending.FlowID, pending.ID,
	)
	view.RefreshFormID = "pid-controller-design-form"
	if pending.Kind == "state-space" {
		view.RefreshFormID = "state-controller-design-form"
	} else if pending.Kind == "robust-synthesis" {
		view.RefreshFormID = "robust-controller-design-form"
	}
	if pending.PID != nil {
		gains := pending.PID.Gains
		view.Details = append(view.Details,
			analysisMetricView{Label: "Kp", Value: formatAnalysisNumber(gains.Proportional)},
			analysisMetricView{Label: "Ki", Value: formatAnalysisNumber(gains.Integral)},
			analysisMetricView{Label: "Kd", Value: formatAnalysisNumber(gains.Derivative)},
			analysisMetricView{
				Label: "N", Value: formatAnalysisNumber(gains.FilterCoefficient),
			},
		)
		if pending.PID.TwoDegreeOfFreedom {
			view.Details = append(view.Details,
				analysisMetricView{
					Label: "Setpoint weight",
					Value: formatAnalysisNumber(gains.SetpointWeight),
				},
				analysisMetricView{
					Label: "Derivative weight",
					Value: formatAnalysisNumber(gains.DerivativeWeight),
				},
			)
		}
	}
	if pending.State != nil {
		diagnostics := pending.State.Diagnostics
		view.Details = append(view.Details,
			analysisMetricView{
				Label: "Controllability",
				Value: fmt.Sprintf("%d / %d", diagnostics.ControllableRank, diagnostics.States),
			},
			analysisMetricView{
				Label: "Observability",
				Value: fmt.Sprintf("%d / %d", diagnostics.ObservableRank, diagnostics.States),
			},
			analysisMetricView{
				Label: "Controller poles",
				Value: strconv.Itoa(len(pending.State.ClosedLoopPoles)),
			},
			analysisMetricView{
				Label: "Estimator poles",
				Value: strconv.Itoa(len(pending.State.EstimatorPoles)),
			},
		)
	}
	if pending.Robust != nil {
		evidence := pending.Robust.Evidence
		view.Details = append(view.Details,
			analysisMetricView{
				Label: "Achieved norm",
				Value: formatAnalysisNumber(evidence.AchievedNorm),
			},
			analysisMetricView{
				Label: "Stable closed loop",
				Value: strconv.FormatBool(evidence.StableClosedLoop),
			},
			analysisMetricView{
				Label: "Closed-loop poles",
				Value: strconv.Itoa(len(evidence.ClosedLoopPoles)),
			},
		)
		if pending.Robust.Method == studio.RobustSynthesisHinf {
			view.Details = append(view.Details,
				analysisMetricView{
					Label: "Gamma bound",
					Value: formatAnalysisNumber(evidence.GammaBound),
				},
				analysisMetricView{
					Label: "Peak frequency",
					Value: formatAnalysisNumber(evidence.PeakFrequency) + " rad/s",
				},
			)
		}
	}
	appendControllerComparisons(&view, review)
	appendControllerPlots(&view, review)
	return view
}

func appendControllerComparisons(
	view *controllerCandidateView,
	review studio.ControllerCandidateReview,
) {
	current := review.Robustness.Current
	candidate := review.Robustness.Candidate
	if candidate == nil {
		return
	}
	if current.ClassicalMargin != nil && candidate.ClassicalMargin != nil {
		view.Comparisons = append(view.Comparisons,
			controllerComparisonMetricView{
				Label: "Phase margin",
				Current: formatOptionalAnalysisNumber(
					current.ClassicalMargin.PhaseMarginDegrees, "unbounded", "°",
				),
				Candidate: formatOptionalAnalysisNumber(
					candidate.ClassicalMargin.PhaseMarginDegrees, "unbounded", "°",
				),
			},
			controllerComparisonMetricView{
				Label: "Gain margin",
				Current: formatOptionalAnalysisNumber(
					current.ClassicalMargin.GainMarginDB, "unbounded", " dB",
				),
				Candidate: formatOptionalAnalysisNumber(
					candidate.ClassicalMargin.GainMarginDB, "unbounded", " dB",
				),
			},
		)
	}
	if current.DiskMargin != nil && candidate.DiskMargin != nil {
		view.Comparisons = append(view.Comparisons,
			controllerComparisonMetricView{
				Label: "Peak sensitivity",
				Current: formatOptionalAnalysisNumber(
					current.DiskMargin.PeakSensitivity, "undefined", "",
				),
				Candidate: formatOptionalAnalysisNumber(
					candidate.DiskMargin.PeakSensitivity, "undefined", "",
				),
			},
		)
	}
	view.Comparisons = append(view.Comparisons,
		controllerComparisonMetricView{
			Label: "Closed-loop bandwidth",
			Current: formatOptionalAnalysisNumber(
				current.ClosedLoopBandwidth, "unbounded", " rad/s",
			),
			Candidate: formatOptionalAnalysisNumber(
				candidate.ClosedLoopBandwidth, "unbounded", " rad/s",
			),
		},
	)
}

func appendControllerPlots(
	view *controllerCandidateView,
	review studio.ControllerCandidateReview,
) {
	for index, trace := range review.Time.Traces {
		if index >= 4 {
			break
		}
		name := trace.OutputName + " ← " + trace.InputName
		view.Plots = append(view.Plots, analysisLinePlot(
			"Closed-loop step: "+name, "time (s)", "output",
			[]analysisSeries{
				{
					Name: "Current", Color: "#5277a8",
					X: review.Time.Times, Y: trace.CurrentValues,
				},
				{
					Name: "Candidate", Color: "#e17845",
					X: review.Time.Times, Y: trace.CandidateValues,
				},
			},
			nil,
		))
	}
	candidate := review.Robustness.Candidate
	if candidate == nil {
		return
	}
	currentBode := review.Robustness.Current.OutputComplementarySensitivity.Bode
	candidateBode := candidate.OutputComplementarySensitivity.Bode
	if len(currentBode) == 0 || len(candidateBode) == 0 {
		return
	}
	view.Plots = append(view.Plots, analysisLinePlot(
		"Complementary sensitivity", "log₁₀ ω", "dB",
		[]analysisSeries{
			{
				Name: "Current", Color: "#5277a8",
				X: transformedFrequencies(review.Robustness.Grid.Omega),
				Y: pointerValues(currentBode[0].MagnitudeDB),
			},
			{
				Name: "Candidate", Color: "#e17845",
				X: transformedFrequencies(review.Robustness.Grid.Omega),
				Y: pointerValues(candidateBode[0].MagnitudeDB),
			},
		},
		nil,
	))
}
