package studio

import (
	"fmt"
	"math"
	"math/cmplx"
	"strings"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

type ModelStudySourceKind string

const (
	ModelStudyStateSpace ModelStudySourceKind = "state-space"
	ModelStudyFRD        ModelStudySourceKind = "frd"
)

type ModelStudyProvenance struct {
	Name             string               `json:"name"`
	Kind             ModelStudySourceKind `json:"kind"`
	Order            int                  `json:"order"`
	Inputs           int                  `json:"inputs"`
	Outputs          int                  `json:"outputs"`
	InputNames       []string             `json:"inputNames,omitempty"`
	OutputNames      []string             `json:"outputNames,omitempty"`
	StateNames       []string             `json:"stateNames,omitempty"`
	SampleTime       float64              `json:"sampleTime"`
	Poles            []ComplexValue       `json:"poles,omitempty"`
	Stable           *bool                `json:"stable,omitempty"`
	Descriptor       bool                 `json:"descriptor"`
	Delayed          bool                 `json:"delayed"`
	FrequencySamples int                  `json:"frequencySamples,omitempty"`
	PoleIssue        string               `json:"poleIssue,omitempty"`
}

type ModelStudy struct {
	system     *controlsys.System
	frd        *controlsys.FRD
	provenance ModelStudyProvenance
}

func NewStateSpaceModelStudy(name string, system *controlsys.System) (*ModelStudy, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("model study source name is required")
	}
	if system == nil {
		return nil, fmt.Errorf("model study source %q is nil", name)
	}
	if err := system.Validate(); err != nil {
		return nil, fmt.Errorf("model study source %q: %w", name, err)
	}
	owned := system.Copy()
	return &ModelStudy{
		system:     owned,
		provenance: stateSpaceProvenance(name, owned),
	}, nil
}

func NewFRDModelStudy(name string, frd *controlsys.FRD) (*ModelStudy, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("model study source name is required")
	}
	if frd == nil {
		return nil, fmt.Errorf("model study source %q is nil", name)
	}
	owned := frd.Copy()
	inputs, outputs := frdDimensions(owned)
	return &ModelStudy{
		frd: owned,
		provenance: ModelStudyProvenance{
			Name:             name,
			Kind:             ModelStudyFRD,
			Inputs:           inputs,
			Outputs:          outputs,
			InputNames:       append([]string(nil), owned.InputName...),
			OutputNames:      append([]string(nil), owned.OutputName...),
			SampleTime:       owned.Dt,
			FrequencySamples: len(owned.Omega),
		},
	}, nil
}

func (study *ModelStudy) Provenance() ModelStudyProvenance {
	return copyModelStudyProvenance(study.provenance)
}

func (study *ModelStudy) SourceSystem() *controlsys.System {
	if study == nil || study.system == nil {
		return nil
	}
	return study.system.Copy()
}

func (study *ModelStudy) SourceFRD() *controlsys.FRD {
	if study == nil || study.frd == nil {
		return nil
	}
	return study.frd.Copy()
}

type ModelStudyCapability string

const (
	ModelStudyReduction    ModelStudyCapability = "reduction"
	ModelStudyEnergy       ModelStudyCapability = "energy"
	ModelStudyStabilitySep ModelStudyCapability = "stability-separation"
	ModelStudyCovariance   ModelStudyCapability = "covariance"
	ModelStudyPassivity    ModelStudyCapability = "sampled-passivity"
)

type ModelStudyCapabilityAssessment struct {
	Capability ModelStudyCapability `json:"capability"`
	Available  bool                 `json:"available"`
	Limitation string               `json:"limitation,omitempty"`
}

func (study *ModelStudy) Capability(capability ModelStudyCapability) ModelStudyCapabilityAssessment {
	assessment := ModelStudyCapabilityAssessment{Capability: capability, Available: true}
	if study == nil {
		assessment.Available = false
		assessment.Limitation = "model study is nil"
		return assessment
	}
	if capability == ModelStudyPassivity && study.frd != nil {
		if study.provenance.Inputs != study.provenance.Outputs {
			assessment.Available = false
			assessment.Limitation = "sampled passivity requires a square response"
		}
		return assessment
	}
	if study.system == nil {
		assessment.Available = false
		assessment.Limitation = fmt.Sprintf("%s requires a state-space source", capability)
		return assessment
	}
	if study.system.IsDescriptor() {
		assessment.Available = false
		assessment.Limitation = fmt.Sprintf("%s does not support descriptor models; convert to an explicit realization first", capability)
		return assessment
	}
	if study.system.HasDelay() || study.system.HasInternalDelay() {
		assessment.Available = false
		assessment.Limitation = fmt.Sprintf("%s does not support exact delays; create an explicit approximation first", capability)
		return assessment
	}
	if capability == ModelStudyPassivity {
		_, inputs, outputs := study.system.Dims()
		if inputs != outputs {
			assessment.Available = false
			assessment.Limitation = "sampled passivity requires a square model"
			return assessment
		}
	}
	switch capability {
	case ModelStudyEnergy, ModelStudyCovariance, ModelStudyPassivity:
		stable, err := study.system.IsStable()
		if err != nil {
			assessment.Available = false
			assessment.Limitation = fmt.Sprintf("%s cannot determine source stability: %v", capability, err)
		} else if !stable {
			assessment.Available = false
			assessment.Limitation = fmt.Sprintf("%s requires a stable source", capability)
		}
	}
	return assessment
}

type ModelEnergyRequest struct {
	InputNoiseCovariance *mat.Dense
}

type ModelEnergyEvidence struct {
	H2Norm              float64            `json:"h2Norm"`
	HinfNorm            float64            `json:"hinfNorm"`
	HinfPeakFrequency   float64            `json:"hinfPeakFrequency"`
	HankelSingular      []float64          `json:"hankelSingularValues"`
	BalancedSingular    []float64          `json:"balancedHankelSingularValues"`
	OutputCovariance    [][]float64        `json:"outputCovariance,omitempty"`
	BalancedRealization *controlsys.System `json:"-"`
}

func (study *ModelStudy) Energy(request ModelEnergyRequest) (ModelEnergyEvidence, error) {
	if err := study.require(ModelStudyEnergy, "energy analysis"); err != nil {
		return ModelEnergyEvidence{}, err
	}
	if request.InputNoiseCovariance != nil {
		if err := study.require(ModelStudyCovariance, "covariance analysis"); err != nil {
			return ModelEnergyEvidence{}, err
		}
	}
	stable, err := study.system.IsStable()
	if err != nil {
		return ModelEnergyEvidence{}, fmt.Errorf("energy analysis: determine stability: %w", err)
	}
	if !stable {
		return ModelEnergyEvidence{}, fmt.Errorf("energy analysis requires a stable source")
	}
	h2, err := controlsys.H2Norm(study.system)
	if err != nil {
		return ModelEnergyEvidence{}, fmt.Errorf("energy analysis H2 norm: %w", err)
	}
	hinf, peak, err := controlsys.HinfNorm(study.system)
	if err != nil {
		return ModelEnergyEvidence{}, fmt.Errorf("energy analysis Hinf norm: %w", err)
	}
	hsv, err := controlsys.HSV(study.system)
	if err != nil {
		return ModelEnergyEvidence{}, fmt.Errorf("energy analysis HSV: %w", err)
	}
	minimal, err := study.system.MinimalRealization()
	if err != nil {
		return ModelEnergyEvidence{}, fmt.Errorf("energy analysis minimal realization for balancing: %w", err)
	}
	balanced, err := controlsys.Balreal(minimal.Sys)
	if err != nil {
		return ModelEnergyEvidence{}, fmt.Errorf("energy analysis balanced realization: %w", err)
	}
	evidence := ModelEnergyEvidence{
		H2Norm:              h2,
		HinfNorm:            hinf,
		HinfPeakFrequency:   peak,
		HankelSingular:      append([]float64(nil), hsv...),
		BalancedSingular:    append([]float64(nil), balanced.HSV...),
		BalancedRealization: balanced.Sys.Copy(),
	}
	if request.InputNoiseCovariance != nil {
		covariance, err := controlsys.Covar(study.system, request.InputNoiseCovariance)
		if err != nil {
			return ModelEnergyEvidence{}, fmt.Errorf("energy analysis covariance: %w", err)
		}
		evidence.OutputCovariance = denseRows(covariance)
	}
	return evidence, nil
}

type ModelPassivityEvidence struct {
	Status                 controlsys.PassivityStatus `json:"status"`
	Passive                bool                       `json:"passive"`
	SampledEvidence        bool                       `json:"sampledEvidence"`
	AnalyticCertificate    bool                       `json:"analyticCertificate"`
	MinHermitianEigenvalue float64                    `json:"minHermitianEigenvalue"`
	Frequency              float64                    `json:"frequency"`
	Omega                  []float64                  `json:"omega"`
	Tolerance              float64                    `json:"tolerance"`
}

func (study *ModelStudy) Passivity(options *controlsys.PassivityOptions) (ModelPassivityEvidence, error) {
	if err := study.require(ModelStudyPassivity, "sampled passivity"); err != nil {
		return ModelPassivityEvidence{}, err
	}
	var (
		result *controlsys.PassivityResult
		err    error
	)
	if study.frd != nil {
		result, err = controlsys.FRDPassive(study.frd, options)
	} else {
		result, err = controlsys.SampledPassive(study.system, options)
	}
	if err != nil {
		return ModelPassivityEvidence{}, fmt.Errorf("sampled passivity: %w", err)
	}
	return ModelPassivityEvidence{
		Status:                 result.Status,
		Passive:                result.Passive,
		SampledEvidence:        true,
		AnalyticCertificate:    false,
		MinHermitianEigenvalue: result.MinHermitianPart,
		Frequency:              result.Frequency,
		Omega:                  append([]float64(nil), result.Omega...),
		Tolerance:              result.Tolerance,
	}, nil
}

type ModelReductionMethod string

const (
	ModelMinimalRealization      ModelReductionMethod = "minimal-realization"
	ModelStaircaseReduction      ModelReductionMethod = "staircase"
	ModelBalancedTruncation      ModelReductionMethod = "balanced-truncation"
	ModelBalancedResidualization ModelReductionMethod = "balanced-residualization"
	ModelStateTruncation         ModelReductionMethod = "state-truncation"
	ModelStateResidualization    ModelReductionMethod = "state-residualization"
	ModelModalTruncation         ModelReductionMethod = "modal-truncation"
)

type ModelReductionRequest struct {
	Method      ModelReductionMethod
	Order       int
	Eliminate   []int
	ReduceMode  controlsys.ReduceMode
	Tolerance   float64
	Equalize    bool
	MaxRealPart float64
}

type ModelFrequencyErrorEvidence struct {
	Omega              []float64 `json:"omega"`
	MaxFrobeniusError  float64   `json:"maxFrobeniusError"`
	PeakErrorFrequency float64   `json:"peakErrorFrequency"`
}

type ModelReductionCandidate struct {
	Method                ModelReductionMethod        `json:"method"`
	Source                ModelStudyProvenance        `json:"source"`
	RetainedOrder         int                         `json:"retainedOrder"`
	InputNames            []string                    `json:"inputNames,omitempty"`
	OutputNames           []string                    `json:"outputNames,omitempty"`
	StateNames            []string                    `json:"stateNames,omitempty"`
	SampleTime            float64                     `json:"sampleTime"`
	Poles                 []ComplexValue              `json:"poles,omitempty"`
	Stable                bool                        `json:"stable"`
	HankelSingularValues  []float64                   `json:"hankelSingularValues,omitempty"`
	DiscardedHankelValues []float64                   `json:"discardedHankelSingularValues,omitempty"`
	HinfErrorBound        *float64                    `json:"hinfErrorBound,omitempty"`
	FrequencyError        ModelFrequencyErrorEvidence `json:"frequencyError"`
	System                *controlsys.System          `json:"-"`
}

func (study *ModelStudy) Reduce(request ModelReductionRequest) (ModelReductionCandidate, error) {
	if err := study.require(ModelStudyReduction, string(request.Method)); err != nil {
		return ModelReductionCandidate{}, err
	}
	var (
		reduced *controlsys.System
		hsv     []float64
		err     error
	)
	switch request.Method {
	case ModelMinimalRealization:
		var result *controlsys.ReduceResult
		result, err = study.system.MinimalRealization()
		if result != nil {
			reduced = result.Sys
		}
	case ModelStaircaseReduction:
		var result *controlsys.ReduceResult
		result, err = study.system.Reduce(&controlsys.ReduceOpts{
			Mode: request.ReduceMode, Tol: request.Tolerance, Equalize: request.Equalize,
		})
		if result != nil {
			reduced = result.Sys
		}
	case ModelBalancedTruncation, ModelBalancedResidualization:
		if err := study.requireStableReduction(request.Method); err != nil {
			return ModelReductionCandidate{}, err
		}
		minimal, minimalErr := study.system.MinimalRealization()
		if minimalErr != nil {
			return ModelReductionCandidate{}, fmt.Errorf("%s minimal realization: %w", request.Method, minimalErr)
		}
		method := controlsys.Truncate
		if request.Method == ModelBalancedResidualization {
			method = controlsys.SingularPerturbation
		}
		reduced, hsv, err = controlsys.Balred(minimal.Sys, request.Order, method)
	case ModelStateTruncation, ModelStateResidualization:
		method := controlsys.Truncate
		if request.Method == ModelStateResidualization {
			method = controlsys.SingularPerturbation
		}
		reduced, err = controlsys.Modred(study.system, request.Eliminate, method)
	case ModelModalTruncation:
		var result *controlsys.ModalReductionResult
		result, err = controlsys.ModalTruncate(study.system, &controlsys.ModalTruncateOptions{
			Order: request.Order, MaxRealPart: request.MaxRealPart,
		})
		if result != nil {
			reduced = result.Sys
		}
	default:
		return ModelReductionCandidate{}, fmt.Errorf("unknown model reduction method %q", request.Method)
	}
	if err != nil {
		return ModelReductionCandidate{}, fmt.Errorf("%s: %w", request.Method, err)
	}
	if reduced == nil {
		return ModelReductionCandidate{}, fmt.Errorf("%s returned no candidate", request.Method)
	}
	candidate, err := study.reductionCandidate(request.Method, reduced, hsv)
	if err != nil {
		return ModelReductionCandidate{}, err
	}
	if request.Method == ModelBalancedTruncation && candidate.RetainedOrder < len(hsv) {
		discarded := append([]float64(nil), hsv[candidate.RetainedOrder:]...)
		bound := 0.0
		for _, value := range discarded {
			bound += 2 * value
		}
		candidate.DiscardedHankelValues = discarded
		candidate.HinfErrorBound = &bound
	}
	return candidate, nil
}

type ModelStudyComponent struct {
	Role        string             `json:"role"`
	Order       int                `json:"order"`
	InputNames  []string           `json:"inputNames,omitempty"`
	OutputNames []string           `json:"outputNames,omitempty"`
	StateNames  []string           `json:"stateNames,omitempty"`
	SampleTime  float64            `json:"sampleTime"`
	Poles       []ComplexValue     `json:"poles,omitempty"`
	Stable      bool               `json:"stable"`
	System      *controlsys.System `json:"-"`
}

type ModelStabilityDecomposition struct {
	Source   ModelStudyProvenance `json:"source"`
	Stable   ModelStudyComponent  `json:"stable"`
	Unstable ModelStudyComponent  `json:"unstable"`
}

func (study *ModelStudy) SeparateStability() (ModelStabilityDecomposition, error) {
	if err := study.require(ModelStudyStabilitySep, "stability separation"); err != nil {
		return ModelStabilityDecomposition{}, err
	}
	result, err := controlsys.Stabsep(study.system)
	if err != nil {
		return ModelStabilityDecomposition{}, fmt.Errorf("stability separation: %w", err)
	}
	stable, err := modelStudyComponent("stable", result.Stable)
	if err != nil {
		return ModelStabilityDecomposition{}, err
	}
	unstable, err := modelStudyComponent("unstable", result.Unstable)
	if err != nil {
		return ModelStabilityDecomposition{}, err
	}
	return ModelStabilityDecomposition{
		Source: copyModelStudyProvenance(study.provenance),
		Stable: stable, Unstable: unstable,
	}, nil
}

func (study *ModelStudy) require(capability ModelStudyCapability, operation string) error {
	assessment := study.Capability(capability)
	if !assessment.Available {
		return fmt.Errorf("%s unavailable: %s", operation, assessment.Limitation)
	}
	return nil
}

func (study *ModelStudy) requireStableReduction(method ModelReductionMethod) error {
	stable, err := study.system.IsStable()
	if err != nil {
		return fmt.Errorf("%s unavailable: cannot determine source stability: %w", method, err)
	}
	if !stable {
		return fmt.Errorf("%s unavailable: balanced reduction requires a stable source", method)
	}
	return nil
}

func (study *ModelStudy) reductionCandidate(
	method ModelReductionMethod,
	reduced *controlsys.System,
	hsv []float64,
) (ModelReductionCandidate, error) {
	owned := reduced.Copy()
	n, _, _ := owned.Dims()
	if len(owned.StateName) != n {
		owned.StateName = make([]string, n)
		for i := range n {
			owned.StateName[i] = fmt.Sprintf("%s.x%d", method, i+1)
		}
	}
	poles, err := owned.Poles()
	if err != nil {
		return ModelReductionCandidate{}, fmt.Errorf("%s candidate poles: %w", method, err)
	}
	stable, err := owned.IsStable()
	if err != nil {
		return ModelReductionCandidate{}, fmt.Errorf("%s candidate stability: %w", method, err)
	}
	frequencyError, err := compareModelFrequency(study.system, owned)
	if err != nil {
		return ModelReductionCandidate{}, fmt.Errorf("%s candidate frequency evidence: %w", method, err)
	}
	return ModelReductionCandidate{
		Method:               method,
		Source:               copyModelStudyProvenance(study.provenance),
		RetainedOrder:        n,
		InputNames:           append([]string(nil), owned.InputName...),
		OutputNames:          append([]string(nil), owned.OutputName...),
		StateNames:           append([]string(nil), owned.StateName...),
		SampleTime:           owned.Dt,
		Poles:                complexValues(poles),
		Stable:               stable,
		HankelSingularValues: append([]float64(nil), hsv...),
		FrequencyError:       frequencyError,
		System:               owned,
	}, nil
}

func modelStudyComponent(role string, system *controlsys.System) (ModelStudyComponent, error) {
	owned := system.Copy()
	n, _, _ := owned.Dims()
	if len(owned.StateName) != n {
		owned.StateName = make([]string, n)
		for index := range n {
			owned.StateName[index] = fmt.Sprintf("%s.x%d", role, index+1)
		}
	}
	poles, err := owned.Poles()
	if err != nil {
		return ModelStudyComponent{}, fmt.Errorf("%s component poles: %w", role, err)
	}
	stable, err := owned.IsStable()
	if err != nil {
		return ModelStudyComponent{}, fmt.Errorf("%s component stability: %w", role, err)
	}
	return ModelStudyComponent{
		Role: role, Order: n,
		InputNames:  append([]string(nil), owned.InputName...),
		OutputNames: append([]string(nil), owned.OutputName...),
		StateNames:  append([]string(nil), owned.StateName...),
		SampleTime:  owned.Dt, Poles: complexValues(poles), Stable: stable, System: owned,
	}, nil
}

func stateSpaceProvenance(name string, system *controlsys.System) ModelStudyProvenance {
	n, inputs, outputs := system.Dims()
	provenance := ModelStudyProvenance{
		Name: name, Kind: ModelStudyStateSpace,
		Order: n, Inputs: inputs, Outputs: outputs,
		InputNames:  append([]string(nil), system.InputName...),
		OutputNames: append([]string(nil), system.OutputName...),
		StateNames:  append([]string(nil), system.StateName...),
		SampleTime:  system.Dt,
		Descriptor:  system.IsDescriptor(),
		Delayed:     system.HasDelay() || system.HasInternalDelay(),
	}
	poles, err := system.Poles()
	if err != nil {
		provenance.PoleIssue = err.Error()
		return provenance
	}
	provenance.Poles = complexValues(poles)
	stable, err := system.IsStable()
	if err != nil {
		provenance.PoleIssue = err.Error()
		return provenance
	}
	provenance.Stable = &stable
	return provenance
}

func copyModelStudyProvenance(source ModelStudyProvenance) ModelStudyProvenance {
	result := source
	result.InputNames = append([]string(nil), source.InputNames...)
	result.OutputNames = append([]string(nil), source.OutputNames...)
	result.StateNames = append([]string(nil), source.StateNames...)
	result.Poles = append([]ComplexValue(nil), source.Poles...)
	if source.Stable != nil {
		stable := *source.Stable
		result.Stable = &stable
	}
	return result
}

func frdDimensions(frd *controlsys.FRD) (inputs, outputs int) {
	if frd == nil || len(frd.Response) == 0 {
		return 0, 0
	}
	outputs = len(frd.Response[0])
	if outputs > 0 {
		inputs = len(frd.Response[0][0])
	}
	return inputs, outputs
}

func denseRows(matrix *mat.Dense) [][]float64 {
	rows, columns := matrix.Dims()
	values := make([][]float64, rows)
	for row := range rows {
		values[row] = make([]float64, columns)
		for column := range columns {
			values[row][column] = matrix.At(row, column)
		}
	}
	return values
}

func compareModelFrequency(source, candidate *controlsys.System) (ModelFrequencyErrorEvidence, error) {
	omega := modelStudyFrequencyGrid(source)
	sourceResponse, err := source.FreqResponse(omega)
	if err != nil {
		return ModelFrequencyErrorEvidence{}, err
	}
	candidateResponse, err := candidate.FreqResponse(omega)
	if err != nil {
		return ModelFrequencyErrorEvidence{}, err
	}
	_, inputs, outputs := source.Dims()
	maximum := 0.0
	peak := omega[0]
	for frequency := range omega {
		sum := 0.0
		for output := range outputs {
			for input := range inputs {
				difference := sourceResponse.At(frequency, output, input) -
					candidateResponse.At(frequency, output, input)
				absolute := cmplx.Abs(difference)
				sum += absolute * absolute
			}
		}
		value := math.Sqrt(sum)
		if value > maximum {
			maximum = value
			peak = omega[frequency]
		}
	}
	return ModelFrequencyErrorEvidence{
		Omega:              append([]float64(nil), omega...),
		MaxFrobeniusError:  maximum,
		PeakErrorFrequency: peak,
	}, nil
}

func modelStudyFrequencyGrid(system *controlsys.System) []float64 {
	const points = 161
	omega := make([]float64, points)
	if system.IsDiscrete() {
		upper := math.Pi / system.Dt
		for index := range points {
			omega[index] = upper * float64(index) / float64(points-1)
		}
		return omega
	}
	omega[0] = 0
	for index := 1; index < points; index++ {
		exponent := -4.0 + 8.0*float64(index-1)/float64(points-2)
		omega[index] = math.Pow(10, exponent)
	}
	return omega
}
