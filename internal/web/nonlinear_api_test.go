package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestNonlinearAPIRegistersLoadsLinearizesAndRunsEKF(t *testing.T) {
	server, _ := openTestServer(t)
	definition := studio.NonlinearDefinition{
		Ref:              studio.NonlinearDefinitionRef{Key: "api/decay", Version: 1},
		Name:             "API decay",
		StateNames:       []string{"x"},
		InputNames:       []string{"u"},
		OutputNames:      []string{"y"},
		Dynamics:         []string{"-0.1*x + u"},
		Outputs:          []string{"x"},
		SampleTime:       0.1,
		IntegrationSteps: 2,
	}

	registered := requestJSONAPI(t, server, http.MethodPost, "/api/v1/nonlinear/definitions", definition)
	if registered.Code != http.StatusCreated {
		t.Fatalf("register status = %d: %s", registered.Code, registered.Body.String())
	}
	var got studio.NonlinearDefinition
	decodeJSONResponse(t, registered, &got)
	if got.Ref != definition.Ref || got.Dynamics[0] != definition.Dynamics[0] {
		t.Fatalf("registered definition = %#v", got)
	}

	loaded := requestAPI(t, server, http.MethodGet, "/api/v1/nonlinear/definitions?key=api%2Fdecay&version=1")
	if loaded.Code != http.StatusOK {
		t.Fatalf("load status = %d: %s", loaded.Code, loaded.Body.String())
	}
	decodeJSONResponse(t, loaded, &got)
	if got.Ref != definition.Ref || got.Outputs[0] != "x" {
		t.Fatalf("loaded definition = %#v", got)
	}

	linearized := requestJSONAPI(t, server, http.MethodPost, "/api/v1/nonlinear/linearizations", studio.NonlinearLinearizationRequest{
		OperatingPoint: studio.NamedOperatingPoint{
			Name: "origin", Definition: definition.Ref,
			State: []float64{0}, Input: []float64{0},
		},
		Directions: []studio.NonlinearValidityDirection{{
			Name: "state", StateDelta: []float64{1}, InputDelta: []float64{0}, Radius: 0.1,
		}},
	})
	if linearized.Code != http.StatusCreated {
		t.Fatalf("linearize status = %d: %s", linearized.Code, linearized.Body.String())
	}
	var candidate studio.NonlinearLinearizationCandidate
	decodeJSONResponse(t, linearized, &candidate)
	if candidate.EquilibriumNorm != 0 || len(candidate.Validity) != 1 || !candidate.CandidateOnly {
		t.Fatalf("linearization = %#v", candidate)
	}

	q, err := studio.NewMatrixValue(1, 1, []float64{0.01})
	if err != nil {
		t.Fatal(err)
	}
	r, err := studio.NewMatrixValue(1, 1, []float64{0.04})
	if err != nil {
		t.Fatal(err)
	}
	p0, err := studio.NewMatrixValue(1, 1, []float64{0.5})
	if err != nil {
		t.Fatal(err)
	}
	run := requestJSONAPI(t, server, http.MethodPost, "/api/v1/nonlinear/ekf", studio.NonlinearEKFRunRequest{
		Estimator: studio.NonlinearEKFDefinition{
			Name: "API estimator", Model: definition.Ref,
			InitialState: []float64{0}, ProcessNoise: q,
			MeasurementNoise: r, InitialCovariance: p0,
		},
		Inputs: [][]float64{{1}}, Measurements: [][]float64{{0.1}},
	})
	if run.Code != http.StatusCreated {
		t.Fatalf("EKF status = %d: %s", run.Code, run.Body.String())
	}
	var ekf studio.NonlinearEKFRun
	decodeJSONResponse(t, run, &ekf)
	if len(ekf.Steps) != 1 || ekf.StateNames[0] != "x" || ekf.MeasurementNames[0] != "y" {
		t.Fatalf("EKF = %#v", ekf)
	}
}

func TestNonlinearAPIRefusesMalformedDefinitionAndMissingQuery(t *testing.T) {
	server, _ := openTestServer(t)
	malformed := studio.NonlinearDefinition{
		Ref:  studio.NonlinearDefinitionRef{Key: "api/bad", Version: 1},
		Name: "Bad", StateNames: []string{"x"}, OutputNames: []string{"y"},
		Dynamics: []string{"x ^ 2"}, Outputs: []string{"x"},
	}
	response := requestJSONAPI(t, server, http.MethodPost, "/api/v1/nonlinear/definitions", malformed)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "pow") {
		t.Fatalf("malformed response = %d: %s", response.Code, response.Body.String())
	}

	missing := requestAPI(t, server, http.MethodGet, "/api/v1/nonlinear/definitions")
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "definition key is required") {
		t.Fatalf("missing query response = %d: %s", missing.Code, missing.Body.String())
	}
}
