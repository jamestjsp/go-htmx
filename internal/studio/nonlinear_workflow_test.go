package studio

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"gonum.org/v1/gonum/mat"
)

func TestNonlinearLinearizationMatchesAnalyticJacobianAndQuadraticLocalError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonlinear.db")
	service := openTestStudio(t, path)
	ctx := context.Background()
	definition := NonlinearDefinition{
		Ref:         NonlinearDefinitionRef{Key: "tests/quadratic", Version: 1},
		Name:        "Quadratic local model",
		StateNames:  []string{"state"},
		InputNames:  []string{"actuation"},
		OutputNames: []string{"measurement"},
	}
	callbacks := scalarQuadraticCallbacks()
	if _, err := service.RegisterNonlinearDefinition(ctx, definition, callbacks); err != nil {
		t.Fatal(err)
	}

	candidate, err := service.LinearizeNonlinear(
		ctx,
		NonlinearLinearizationRequest{
			OperatingPoint: NamedOperatingPoint{
				Name: "origin", Definition: definition.Ref,
				State: []float64{0}, Input: []float64{0},
			},
			Directions: []NonlinearValidityDirection{{
				Name: "positive state", StateDelta: []float64{1},
				InputDelta: []float64{0}, Radius: 0.1,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !candidate.CandidateOnly || candidate.System == nil {
		t.Fatalf("linearization candidate = %#v", candidate)
	}
	if math.Abs(candidate.System.A.At(0, 0)+2) > 1e-7 ||
		math.Abs(candidate.System.B.At(0, 0)-1) > 1e-7 ||
		math.Abs(candidate.System.C.At(0, 0)-1) > 1e-7 ||
		math.Abs(candidate.System.D.At(0, 0)-2) > 1e-7 {
		t.Fatalf(
			"linearization A=%g B=%g C=%g D=%g",
			candidate.System.A.At(0, 0), candidate.System.B.At(0, 0),
			candidate.System.C.At(0, 0), candidate.System.D.At(0, 0),
		)
	}
	if got := candidate.System.StateName; len(got) != 1 || got[0] != "state" {
		t.Fatalf("state names = %v", got)
	}
	if candidate.EquilibriumNorm != 0 ||
		len(candidate.EquilibriumResidual) != 1 ||
		candidate.Provenance.Definition != definition.Ref ||
		candidate.Provenance.OperatingPointName != "origin" {
		t.Fatalf("operating-point provenance = %#v", candidate)
	}
	validity := candidate.Validity[0]
	if math.Abs(validity.StateErrorNorm-0.01) > 1e-9 ||
		math.Abs(validity.OutputErrorNorm-0.005) > 1e-9 ||
		validity.StateQuadraticRatio == nil ||
		math.Abs(*validity.StateQuadraticRatio-4) > 1e-7 ||
		validity.OutputQuadraticRatio == nil ||
		math.Abs(*validity.OutputQuadraticRatio-4) > 1e-7 {
		t.Fatalf("directional validity = %#v", validity)
	}

	changed := definition
	changed.Name = "Changed without a version"
	if _, err := service.RegisterNonlinearDefinition(ctx, changed, callbacks); err == nil ||
		!strings.Contains(err.Error(), "increment its version") {
		t.Fatalf("stable definition overwrite error = %v", err)
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
		persisted.StateNames[0] != definition.StateNames[0] {
		t.Fatalf("persisted definition = %#v", persisted)
	}
}

func TestNonlinearLinearizationRefusesNonEquilibriumAndBadDimensions(t *testing.T) {
	service := openTestStudio(t, filepath.Join(t.TempDir(), "nonlinear-errors.db"))
	ctx := context.Background()
	definition := NonlinearDefinition{
		Ref:         NonlinearDefinitionRef{Key: "tests/refusal", Version: 1},
		Name:        "Refusal fixture",
		StateNames:  []string{"state"},
		InputNames:  []string{"input"},
		OutputNames: []string{"output"},
	}
	if _, err := service.RegisterNonlinearDefinition(
		ctx, definition, scalarQuadraticCallbacks(),
	); err != nil {
		t.Fatal(err)
	}
	_, err := service.LinearizeNonlinear(ctx, NonlinearLinearizationRequest{
		OperatingPoint: NamedOperatingPoint{
			Name: "not equilibrium", Definition: definition.Ref,
			State: []float64{0.2}, Input: []float64{0},
			EquilibriumTolerance: 1e-9,
		},
		Directions: []NonlinearValidityDirection{{
			Name: "state", StateDelta: []float64{1},
			InputDelta: []float64{0}, Radius: 0.1,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "not an equilibrium") ||
		!strings.Contains(err.Error(), "residual norm") {
		t.Fatalf("non-equilibrium error = %v", err)
	}

	_, err = service.LinearizeNonlinear(ctx, NonlinearLinearizationRequest{
		OperatingPoint: NamedOperatingPoint{
			Name: "wrong state", Definition: definition.Ref,
			State: []float64{0, 1}, Input: []float64{0},
		},
		Directions: []NonlinearValidityDirection{{
			Name: "state", StateDelta: []float64{1},
			InputDelta: []float64{0}, Radius: 0.1,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "operating state has length 2; want 1") {
		t.Fatalf("operating dimension error = %v", err)
	}

	badCallbacks := scalarQuadraticCallbacks()
	badCallbacks.Dynamics = func(x, u *mat.VecDense) *mat.VecDense {
		return mat.NewVecDense(2, nil)
	}
	badDefinition := definition
	badDefinition.Ref.Version = 2
	if _, err := service.RegisterNonlinearDefinition(
		ctx, badDefinition, badCallbacks,
	); err != nil {
		t.Fatal(err)
	}
	_, err = service.LinearizeNonlinear(ctx, NonlinearLinearizationRequest{
		OperatingPoint: NamedOperatingPoint{
			Name: "bad callback", Definition: badDefinition.Ref,
			State: []float64{0}, Input: []float64{0},
		},
		Directions: []NonlinearValidityDirection{{
			Name: "state", StateDelta: []float64{1},
			InputDelta: []float64{0}, Radius: 0.1,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "returned length 2; want 1") {
		t.Fatalf("callback dimension error = %v", err)
	}
}

func TestNonlinearEKFBatchMatchesLinearKalmanFixture(t *testing.T) {
	service := openTestStudio(t, filepath.Join(t.TempDir(), "ekf.db"))
	ctx := context.Background()
	const a, b, c = 0.9, 0.2, 1.0
	definition := NonlinearDefinition{
		Ref:         NonlinearDefinitionRef{Key: "tests/linear-ekf", Version: 1},
		Name:        "Linear EKF oracle",
		StateNames:  []string{"state"},
		InputNames:  []string{"control"},
		OutputNames: []string{"sensor"},
	}
	callbacks := NonlinearRuntimeCallbacks{
		Dynamics: func(x, u *mat.VecDense) *mat.VecDense {
			return mat.NewVecDense(1, []float64{-0.1*x.AtVec(0) + b*u.AtVec(0)})
		},
		Output: func(x, u *mat.VecDense) *mat.VecDense {
			return mat.NewVecDense(1, []float64{c * x.AtVec(0)})
		},
		Transition: func(x, u *mat.VecDense) *mat.VecDense {
			return mat.NewVecDense(1, []float64{a*x.AtVec(0) + b*u.AtVec(0)})
		},
		Measurement: func(x *mat.VecDense) *mat.VecDense {
			return mat.NewVecDense(1, []float64{c * x.AtVec(0)})
		},
		TransitionJacobian: func(x, u *mat.VecDense) *mat.Dense {
			return mat.NewDense(1, 1, []float64{a})
		},
		MeasurementJacobian: func(x *mat.VecDense) *mat.Dense {
			return mat.NewDense(1, 1, []float64{c})
		},
	}
	if _, err := service.RegisterNonlinearDefinition(ctx, definition, callbacks); err != nil {
		t.Fatal(err)
	}
	q, r, p0 := 0.01, 0.04, 0.5
	inputs := [][]float64{{1}, {0}, {-0.5}, {0.25}}
	measurements := [][]float64{{0.15}, {0.3}, {0.1}, {0.05}}
	run, err := service.RunNonlinearEKF(ctx, NonlinearEKFRunRequest{
		Estimator: NonlinearEKFDefinition{
			Name: "linear comparison", Model: definition.Ref,
			InitialState:      []float64{0},
			ProcessNoise:      scalarMatrix(t, q),
			MeasurementNoise:  scalarMatrix(t, r),
			InitialCovariance: scalarMatrix(t, p0),
		},
		Inputs: inputs, Measurements: measurements,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Steps) != len(inputs) ||
		run.StateNames[0] != "state" ||
		run.MeasurementNames[0] != "sensor" {
		t.Fatalf("EKF run metadata = %#v", run)
	}
	x, covariance := 0.0, p0
	for i := range inputs {
		predictedX := a*x + b*inputs[i][0]
		predictedP := a*a*covariance + q
		gain := predictedP * c / (c*c*predictedP + r)
		x = predictedX + gain*(measurements[i][0]-c*predictedX)
		covariance = (1-gain*c)*(1-gain*c)*predictedP + gain*gain*r
		step := run.Steps[i]
		if math.Abs(step.PredictedState[0]-predictedX) > 1e-12 ||
			math.Abs(step.PredictedCovariance.At(0, 0)-predictedP) > 1e-12 ||
			math.Abs(step.UpdatedState[0]-x) > 1e-12 ||
			math.Abs(step.UpdatedCovariance.At(0, 0)-covariance) > 1e-12 {
			t.Fatalf(
				"EKF step %d = %#v, oracle x=%g P=%g",
				i, step, x, covariance,
			)
		}
	}
	if math.Abs(run.FinalState[0]-x) > 1e-12 ||
		math.Abs(run.FinalCovariance.At(0, 0)-covariance) > 1e-12 {
		t.Fatalf("EKF final state/covariance = %v %#v", run.FinalState, run.FinalCovariance)
	}
}

func TestNonlinearEKFValidatesBatchDimensionsAndCovariances(t *testing.T) {
	service := openTestStudio(t, filepath.Join(t.TempDir(), "ekf-errors.db"))
	ctx := context.Background()
	definition := NonlinearDefinition{
		Ref:         NonlinearDefinitionRef{Key: "tests/ekf-errors", Version: 1},
		Name:        "EKF validation",
		StateNames:  []string{"x1", "x2"},
		InputNames:  []string{"u"},
		OutputNames: []string{"y"},
	}
	callbacks := twoStateIdentityCallbacks()
	if _, err := service.RegisterNonlinearDefinition(ctx, definition, callbacks); err != nil {
		t.Fatal(err)
	}
	base := NonlinearEKFRunRequest{
		Estimator: NonlinearEKFDefinition{
			Name: "validation", Model: definition.Ref,
			InitialState:      []float64{0, 0},
			ProcessNoise:      denseMatrixValue(t, 2, []float64{1, 0, 0, 1}),
			MeasurementNoise:  scalarMatrix(t, 1),
			InitialCovariance: denseMatrixValue(t, 2, []float64{1, 0, 0, 1}),
		},
		Inputs:       [][]float64{{0}},
		Measurements: [][]float64{{0}},
	}
	badSymmetry := base
	badSymmetry.Estimator.ProcessNoise = denseMatrixValue(
		t, 2, []float64{1, 0.2, 0, 1},
	)
	if _, err := service.RunNonlinearEKF(ctx, badSymmetry); err == nil ||
		!strings.Contains(err.Error(), "must be symmetric") {
		t.Fatalf("asymmetric covariance error = %v", err)
	}
	badPSD := base
	badPSD.Estimator.InitialCovariance = denseMatrixValue(
		t, 2, []float64{1, 0, 0, -0.1},
	)
	if _, err := service.RunNonlinearEKF(ctx, badPSD); err == nil ||
		!strings.Contains(err.Error(), "positive semidefinite") {
		t.Fatalf("indefinite covariance error = %v", err)
	}
	badInput := base
	badInput.Inputs = [][]float64{{0, 1}}
	if _, err := service.RunNonlinearEKF(ctx, badInput); err == nil ||
		!strings.Contains(err.Error(), "input 1 has length 2; want 1") {
		t.Fatalf("input dimension error = %v", err)
	}
	badMeasurement := base
	badMeasurement.Measurements = [][]float64{{0, 1}}
	if _, err := service.RunNonlinearEKF(ctx, badMeasurement); err == nil ||
		!strings.Contains(err.Error(), "measurement 1 has length 2; want 1") {
		t.Fatalf("measurement dimension error = %v", err)
	}
}

func scalarQuadraticCallbacks() NonlinearRuntimeCallbacks {
	return NonlinearRuntimeCallbacks{
		Dynamics: func(x, u *mat.VecDense) *mat.VecDense {
			value := x.AtVec(0)
			return mat.NewVecDense(1, []float64{
				-2*value + u.AtVec(0) + value*value,
			})
		},
		Output: func(x, u *mat.VecDense) *mat.VecDense {
			value := x.AtVec(0)
			return mat.NewVecDense(1, []float64{
				value + 0.5*value*value + 2*u.AtVec(0),
			})
		},
		Transition: func(x, u *mat.VecDense) *mat.VecDense {
			return mat.NewVecDense(1, []float64{x.AtVec(0) + u.AtVec(0)})
		},
		Measurement: func(x *mat.VecDense) *mat.VecDense {
			return mat.NewVecDense(1, []float64{x.AtVec(0)})
		},
		TransitionJacobian: func(x, u *mat.VecDense) *mat.Dense {
			return mat.NewDense(1, 1, []float64{1})
		},
		MeasurementJacobian: func(x *mat.VecDense) *mat.Dense {
			return mat.NewDense(1, 1, []float64{1})
		},
	}
}

func twoStateIdentityCallbacks() NonlinearRuntimeCallbacks {
	return NonlinearRuntimeCallbacks{
		Dynamics: func(x, u *mat.VecDense) *mat.VecDense {
			return mat.VecDenseCopyOf(x)
		},
		Output: func(x, u *mat.VecDense) *mat.VecDense {
			return mat.NewVecDense(1, []float64{x.AtVec(0)})
		},
		Transition: func(x, u *mat.VecDense) *mat.VecDense {
			return mat.VecDenseCopyOf(x)
		},
		Measurement: func(x *mat.VecDense) *mat.VecDense {
			return mat.NewVecDense(1, []float64{x.AtVec(0)})
		},
		TransitionJacobian: func(x, u *mat.VecDense) *mat.Dense {
			return mat.NewDense(2, 2, []float64{1, 0, 0, 1})
		},
		MeasurementJacobian: func(x *mat.VecDense) *mat.Dense {
			return mat.NewDense(1, 2, []float64{1, 0})
		},
	}
}

func scalarMatrix(t *testing.T, value float64) MatrixValue {
	t.Helper()
	return denseMatrixValue(t, 1, []float64{value})
}

func denseMatrixValue(t *testing.T, size int, values []float64) MatrixValue {
	t.Helper()
	matrix, err := NewMatrixValue(size, size, values)
	if err != nil {
		t.Fatal(err)
	}
	return matrix
}
