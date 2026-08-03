package studio

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"gonum.org/v1/gonum/mat"
)

func TestNonlinearDefinitionPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonlinear.db")
	ctx := context.Background()
	definition := nonlinearTestDefinition(
		"tests/restart", []string{"-0.1*x + u"}, []string{"x"},
	)
	definition.SampleTime = 0.1
	service := openTestStudio(t, path)
	if _, err := service.RegisterNonlinearDefinition(ctx, definition); err != nil {
		t.Fatal(err)
	}
	if err := service.db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	persisted, err := reopened.NonlinearDefinition(ctx, definition.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Name != definition.Name ||
		persisted.Dynamics[0] != definition.Dynamics[0] ||
		persisted.Outputs[0] != definition.Outputs[0] ||
		persisted.SampleTime != definition.SampleTime {
		t.Fatalf("persisted definition = %#v", persisted)
	}

	request := NonlinearLinearizationRequest{
		OperatingPoint: NamedOperatingPoint{
			Name: "origin", Definition: definition.Ref,
			State: []float64{0}, Input: []float64{0},
		},
		Directions: []NonlinearValidityDirection{{
			Name: "state", StateDelta: []float64{1},
			InputDelta: []float64{0}, Radius: 0.1,
		}},
	}
	candidate, err := reopened.LinearizeNonlinear(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(candidate.System.A.At(0, 0)+0.1) > 1e-7 ||
		math.Abs(candidate.System.B.At(0, 0)-1) > 1e-7 ||
		math.Abs(candidate.System.C.At(0, 0)-1) > 1e-7 ||
		math.Abs(candidate.System.D.At(0, 0)) > 1e-7 {
		t.Fatalf("linearization A=%g B=%g C=%g D=%g",
			candidate.System.A.At(0, 0), candidate.System.B.At(0, 0),
			candidate.System.C.At(0, 0), candidate.System.D.At(0, 0))
	}
	if candidate.Provenance.RuntimeRegisteredAt.IsZero() {
		t.Fatal("definition provenance has no stored creation time")
	}

	run, err := reopened.RunNonlinearEKF(ctx, NonlinearEKFRunRequest{
		Estimator: NonlinearEKFDefinition{
			Name: "restart estimator", Model: definition.Ref,
			InitialState:      []float64{0},
			ProcessNoise:      scalarMatrix(t, 0.01),
			MeasurementNoise:  scalarMatrix(t, 0.04),
			InitialCovariance: scalarMatrix(t, 0.5),
		},
		Inputs:       [][]float64{{1}},
		Measurements: [][]float64{{0.1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Steps) != 1 || len(run.FinalState) != 1 || run.MeasurementNames[0] != "y" {
		t.Fatalf("restart EKF result = %#v", run)
	}

	changed := definition
	changed.Name = "Changed without a version"
	if _, err := reopened.RegisterNonlinearDefinition(ctx, changed); err == nil ||
		!strings.Contains(err.Error(), "increment its version") {
		t.Fatalf("stable definition overwrite error = %v", err)
	}
}

func TestNonlinearExpressionsProduceAnalyticJacobians(t *testing.T) {
	service := openTestStudio(t, filepath.Join(t.TempDir(), "nonlinear-jacobians.db"))
	ctx := context.Background()
	definition := nonlinearTestDefinition(
		"tests/jacobians", []string{"0.5*x + u"}, []string{"2*x + sin(x)"},
	)
	definition.SampleTime = 0.2
	definition.IntegrationSteps = 4
	if _, err := service.RegisterNonlinearDefinition(ctx, definition); err != nil {
		t.Fatal(err)
	}

	_, runtime, _, err := service.nonlinearRuntimeDefinition(ctx, definition.Ref)
	if err != nil {
		t.Fatal(err)
	}
	stateJacobian := runtime.transitionJacobian(
		vector([]float64{0}), vector([]float64{0}),
	)
	measurementJacobian := runtime.measurementJacobian(vector([]float64{0}))
	if stateJacobian == nil || measurementJacobian == nil {
		t.Fatal("derived Jacobian was nil")
	}
	if math.Abs(stateJacobian.At(0, 0)-math.Exp(0.1)) > 1e-8 {
		t.Fatalf("transition Jacobian = %g, want %g", stateJacobian.At(0, 0), math.Exp(0.1))
	}
	if math.Abs(measurementJacobian.At(0, 0)-3) > 1e-8 {
		t.Fatalf("measurement Jacobian = %g, want 3", measurementJacobian.At(0, 0))
	}

	candidate, err := service.LinearizeNonlinear(ctx, NonlinearLinearizationRequest{
		OperatingPoint: NamedOperatingPoint{
			Name: "origin", Definition: definition.Ref,
			State: []float64{0}, Input: []float64{0},
		},
		Directions: []NonlinearValidityDirection{{
			Name: "positive", StateDelta: []float64{1},
			InputDelta: []float64{0}, Radius: 0.1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(candidate.System.A.At(0, 0)-0.5) > 1e-7 ||
		math.Abs(candidate.System.B.At(0, 0)-1) > 1e-7 ||
		math.Abs(candidate.System.C.At(0, 0)-3) > 1e-7 {
		t.Fatalf("continuous Jacobians = A=%g B=%g C=%g",
			candidate.System.A.At(0, 0), candidate.System.B.At(0, 0), candidate.System.C.At(0, 0))
	}
}

func TestNonlinearExpressionValidationAndEKFBoundaries(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		edit func(*NonlinearDefinition)
		want string
	}{
		{name: "power operator", edit: func(definition *NonlinearDefinition) {
			definition.Dynamics[0] = "x^2"
		}, want: "pow"},
		{name: "unknown signal", edit: func(definition *NonlinearDefinition) {
			definition.Dynamics[0] = "missing + x"
		}, want: "missing"},
		{name: "invalid signal name", edit: func(definition *NonlinearDefinition) {
			definition.StateNames[0] = "not valid"
		}, want: "not valid"},
		{name: "unknown function", edit: func(definition *NonlinearDefinition) {
			definition.Dynamics[0] = "floor(x)"
		}, want: "floor"},
		{name: "reserved constant", edit: func(definition *NonlinearDefinition) {
			definition.StateNames[0] = "e"
		}, want: "e"},
		{name: "unsupported syntax", edit: func(definition *NonlinearDefinition) {
			definition.Dynamics[0] = "x[0]"
		}, want: "x[0]"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := openTestStudio(t, filepath.Join(t.TempDir(), "validation.db"))
			definition := nonlinearTestDefinition(
				"tests/validation", []string{"-x"}, []string{"x"},
			)
			definition.Ref.Version = index + 1
			test.edit(&definition)
			if _, err := service.RegisterNonlinearDefinition(ctx, definition); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("registration error = %v, want %q", err, test.want)
			}
		})
	}

	service := openTestStudio(t, filepath.Join(t.TempDir(), "boundaries.db"))
	direct := nonlinearTestDefinition(
		"tests/direct-feedthrough", []string{"-x + u"}, []string{"x + u"},
	)
	direct.SampleTime = 0.1
	if _, err := service.RegisterNonlinearDefinition(ctx, direct); err != nil {
		t.Fatal(err)
	}
	candidate, err := service.LinearizeNonlinear(ctx, NonlinearLinearizationRequest{
		OperatingPoint: NamedOperatingPoint{
			Name: "origin", Definition: direct.Ref,
			State: []float64{0}, Input: []float64{0},
		},
		Directions: []NonlinearValidityDirection{{
			Name: "state", StateDelta: []float64{1},
			InputDelta: []float64{0}, Radius: 0.1,
		}},
	})
	if err != nil || math.Abs(candidate.System.D.At(0, 0)-1) > 1e-7 {
		t.Fatalf("direct-feedthrough linearization = %#v, err=%v", candidate, err)
	}
	_, err = service.RunNonlinearEKF(ctx, baseNonlinearEKFRequest(direct))
	if err == nil || !strings.Contains(err.Error(), "output") || !strings.Contains(err.Error(), "u") {
		t.Fatalf("direct-feedthrough EKF error = %v", err)
	}

	noSampleTime := nonlinearTestDefinition(
		"tests/no-sample-time", []string{"-x"}, []string{"x"},
	)
	if _, err := service.RegisterNonlinearDefinition(ctx, noSampleTime); err != nil {
		t.Fatal(err)
	}
	_, err = service.RunNonlinearEKF(ctx, baseNonlinearEKFRequest(noSampleTime))
	if err == nil || !strings.Contains(err.Error(), "sampleTime") {
		t.Fatalf("missing sample time error = %v", err)
	}
}

func TestNonlinearDefinitionsAreIsolatedAcrossStudios(t *testing.T) {
	ctx := context.Background()
	first := openTestStudio(t, filepath.Join(t.TempDir(), "first.db"))
	second := openTestStudio(t, filepath.Join(t.TempDir(), "second.db"))
	firstDefinition := nonlinearTestDefinition(
		"tests/first", []string{"-x"}, []string{"x"},
	)
	secondDefinition := nonlinearTestDefinition(
		"tests/second", []string{"-x"}, []string{"x"},
	)
	if _, err := first.RegisterNonlinearDefinition(ctx, firstDefinition); err != nil {
		t.Fatal(err)
	}
	if _, err := second.RegisterNonlinearDefinition(ctx, secondDefinition); err != nil {
		t.Fatal(err)
	}
	if _, err := first.NonlinearDefinition(ctx, secondDefinition.Ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first studio saw second definition: %v", err)
	}
	if _, err := second.NonlinearDefinition(ctx, firstDefinition.Ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second studio saw first definition: %v", err)
	}
}

func nonlinearTestDefinition(key string, dynamics, outputs []string) NonlinearDefinition {
	return NonlinearDefinition{
		Ref:         NonlinearDefinitionRef{Key: key, Version: 1},
		Name:        "Expression test definition",
		StateNames:  []string{"x"},
		InputNames:  []string{"u"},
		OutputNames: []string{"y"},
		Dynamics:    dynamics,
		Outputs:     outputs,
	}
}

func baseNonlinearEKFRequest(definition NonlinearDefinition) NonlinearEKFRunRequest {
	return NonlinearEKFRunRequest{
		Estimator: NonlinearEKFDefinition{
			Name: "test estimator", Model: definition.Ref,
			InitialState:      []float64{0},
			ProcessNoise:      mustMatrixValue(1, []float64{0.01}),
			MeasurementNoise:  mustMatrixValue(1, []float64{0.04}),
			InitialCovariance: mustMatrixValue(1, []float64{0.5}),
		},
		Inputs:       [][]float64{{0}},
		Measurements: [][]float64{{0}},
	}
}

func vector(values []float64) *mat.VecDense {
	return mat.NewVecDense(len(values), values)
}

func mustMatrixValue(size int, values []float64) MatrixValue {
	value, err := NewMatrixValue(size, size, values)
	if err != nil {
		panic(err)
	}
	return value
}

func scalarMatrix(t *testing.T, value float64) MatrixValue {
	t.Helper()
	return mustMatrixValue(1, []float64{value})
}
