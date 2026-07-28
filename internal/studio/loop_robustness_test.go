package studio

import (
	"context"
	"encoding/json"
	"math"
	"math/cmplx"
	"slices"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestAnalyzeLoopRobustnessComparesCurrentAndCandidateOnOneGrid(t *testing.T) {
	service, flowID, _, _ := tuningStudio(t)
	candidateController, err := controlsys.NewGain(
		mat.NewDense(1, 1, []float64{2}), 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	omega := []float64{0.1, 1, 10}
	result, err := service.AnalyzeLoopRobustness(
		context.Background(),
		flowID,
		LoopRobustnessRequest{
			Omega: omega, CandidateController: candidateController,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Grid.Source != "explicit" ||
		!slices.Equal(result.Grid.Omega, omega) ||
		result.Candidate == nil {
		t.Fatalf("comparison grid/candidate = %#v", result)
	}
	for label, analysis := range map[string]LoopSensitivityAnalysis{
		"current": result.Current, "candidate": *result.Candidate,
	} {
		for responseName, response := range map[string]LoopResponseAnalysis{
			"So": analysis.OutputSensitivity,
			"To": analysis.OutputComplementarySensitivity,
			"Si": analysis.InputSensitivity,
			"Ti": analysis.InputComplementarySensitivity,
		} {
			if len(response.Bode) != 1 ||
				len(response.Bode[0].MagnitudeDB) != len(omega) ||
				response.SingularValues == nil ||
				len(response.SingularValues.Values) != 1 ||
				len(response.SingularValues.Values[0]) != len(omega) {
				t.Fatalf("%s %s response = %#v", label, responseName, response)
			}
		}
		if analysis.ClassicalMargin == nil || analysis.DiskMargin == nil {
			t.Fatalf("%s SISO robustness metrics = %#v", label, analysis)
		}
	}
	currentMagnitude := result.Current.OutputComplementarySensitivity.Bode[0].MagnitudeDB[1]
	candidateMagnitude := result.Candidate.OutputComplementarySensitivity.Bode[0].MagnitudeDB[1]
	if currentMagnitude == nil || candidateMagnitude == nil ||
		math.Abs(*currentMagnitude-*candidateMagnitude) < 1e-6 {
		t.Fatalf(
			"current/candidate complementary sensitivity did not change: %v %v",
			currentMagnitude, candidateMagnitude,
		)
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatalf("robustness analysis is not JSON-safe: %v", err)
	}
}

func TestLoopSensitivityMIMOIdentitiesAndNamesAgainstMatrixOracle(t *testing.T) {
	plant, err := controlsys.NewGain(mat.NewDense(2, 2, []float64{
		1, 2,
		0, 1,
	}), 0)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := controlsys.NewGain(mat.NewDense(2, 2, []float64{
		1, 0,
		3, 1,
	}), 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = plant.SetInputName("u1", "u2")
	_ = plant.SetOutputName("y1", "y2")
	_ = controller.SetInputName("y1", "y2")
	_ = controller.SetOutputName("u1", "u2")
	result, err := analyzeLoopController(
		plant, controller, []float64{0.1, 1, 10},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(
		result.OutputSensitivity.InputNames, []string{"y1", "y2"},
	) || !equalStrings(
		result.InputSensitivity.InputNames, []string{"u1", "u2"},
	) {
		t.Fatalf(
			"loop-side names = output %v, input %v",
			result.OutputSensitivity.InputNames,
			result.InputSensitivity.InputNames,
		)
	}
	identity := mat.NewDense(2, 2, []float64{1, 0, 0, 1})
	outputSum := mat.NewDense(2, 2, nil)
	outputSum.Add(result.models.So.D, result.models.To.D)
	if !mat.EqualApprox(outputSum, identity, 1e-13) {
		t.Fatalf("So + To =\n%v", mat.Formatted(outputSum))
	}
	inputSum := mat.NewDense(2, 2, nil)
	inputSum.Add(result.models.Si.D, result.models.Ti.D)
	if !mat.EqualApprox(inputSum, identity, 1e-13) {
		t.Fatalf("Si + Ti =\n%v", mat.Formatted(inputSum))
	}
	if mat.EqualApprox(result.models.So.D, result.models.Si.D, 1e-13) {
		t.Fatal("noncommutative MIMO fixture collapsed input and output sensitivity")
	}
	for _, frequency := range []float64{0.1, 1, 10} {
		so, err := result.models.So.FreqResponse([]float64{frequency})
		if err != nil {
			t.Fatal(err)
		}
		to, err := result.models.To.FreqResponse([]float64{frequency})
		if err != nil {
			t.Fatal(err)
		}
		si, err := result.models.Si.FreqResponse([]float64{frequency})
		if err != nil {
			t.Fatal(err)
		}
		ti, err := result.models.Ti.FreqResponse([]float64{frequency})
		if err != nil {
			t.Fatal(err)
		}
		for row := range 2 {
			for column := range 2 {
				want := complex(0, 0)
				if row == column {
					want = 1
				}
				if difference := cmplx.Abs(
					so.At(0, row, column) + to.At(0, row, column) - want,
				); difference > 1e-12 {
					t.Fatalf("So+To identity at %g [%d,%d] differs by %g", frequency, row, column, difference)
				}
				if difference := cmplx.Abs(
					si.At(0, row, column) + ti.At(0, row, column) - want,
				); difference > 1e-12 {
					t.Fatalf("Si+Ti identity at %g [%d,%d] differs by %g", frequency, row, column, difference)
				}
			}
		}
	}
	for _, response := range []LoopResponseAnalysis{
		result.OutputSensitivity,
		result.OutputComplementarySensitivity,
		result.InputSensitivity,
		result.InputComplementarySensitivity,
	} {
		if len(response.Bode) != 4 ||
			response.SingularValues == nil ||
			len(response.SingularValues.Values) != 2 ||
			len(response.SingularValues.Values[0]) != 3 {
			t.Fatalf("MIMO response = %#v", response)
		}
	}
	if result.ClassicalMargin != nil || result.DiskMargin != nil {
		t.Fatalf("MIMO analysis exposed SISO-only margins: %#v", result)
	}
}

func TestAnalyzeLoopRobustnessDiagnosesCandidateAgainstSelectedRoles(t *testing.T) {
	service, flowID, _, _ := tuningStudio(t)
	candidates := make(map[string]*controlsys.System)
	var err error
	candidates["dimensions"], err = controlsys.NewGain(
		mat.NewDense(2, 2, []float64{1, 0, 0, 1}), 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	candidates["time domain"], err = controlsys.NewGain(
		mat.NewDense(1, 1, []float64{1}), 0.1,
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, candidate := range candidates {
		t.Run(name, func(t *testing.T) {
			_, err := service.AnalyzeLoopRobustness(
				context.Background(),
				flowID,
				LoopRobustnessRequest{
					Omega:               []float64{0.1, 1},
					CandidateController: candidate,
				},
			)
			if err == nil ||
				!strings.Contains(err.Error(), "candidate controller") ||
				!strings.Contains(err.Error(), "selected plant/controller roles") {
				t.Fatalf("candidate compatibility error = %v", err)
			}
		})
	}
}
