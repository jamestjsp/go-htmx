package studio

import (
	"errors"
	"math"
	"math/cmplx"
	"slices"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestPhysicalAssemblySpikeExplicitNodeMatchesSignalFlowEquations(t *testing.T) {
	assembled := assemblePhysicalSpikeNode(t, "explicit-node", []controlsys.PhysicalComponent{
		physicalSpikeComponent(t, "vessel", 1, explicitPhysicalSpikePorts()),
		physicalSpikeComponent(t, "jacket", 2, explicitPhysicalSpikePorts()),
	})

	assertPhysicalSpikeDescriptorEquations(t, assembled, []float64{1, 2})
	assertPhysicalSpikeSignalFlowOracle(t, assembled, []float64{1, 2})
	assertPhysicalSpikeTimeSimulationBoundary(t, assembled)
}

func TestPhysicalAssemblySpikeImplicitBindingIsDeclarationOrderIndependent(t *testing.T) {
	explicit := controlsys.PhysicalPort{
		Name: "external", Kind: controlsys.PhysicalPortDisplacement, Dimension: 1,
		Input: []int{0}, Output: []int{0},
	}
	implicit := controlsys.PhysicalPort{
		Name: "node", Kind: controlsys.PhysicalPortDisplacement, Dimension: 1,
	}
	build := func(name string, firstPorts []controlsys.PhysicalPort) *controlsys.System {
		t.Helper()
		return assemblePhysicalSpikeNode(t, name, []controlsys.PhysicalComponent{
			physicalSpikeComponent(t, "feed", 1, firstPorts),
			physicalSpikeComponent(t, "reactor", 2, explicitPhysicalSpikePorts()),
			physicalSpikeComponent(t, "utility", 3, explicitPhysicalSpikePorts()),
		})
	}

	explicitFirst := build("implicit-node", []controlsys.PhysicalPort{explicit, implicit})
	implicitFirst := build("implicit-node", []controlsys.PhysicalPort{implicit, explicit})

	assertPhysicalSpikeDescriptorEquations(t, explicitFirst, []float64{1, 2, 3})
	assertPhysicalSpikeSignalFlowOracle(t, explicitFirst, []float64{1, 2, 3})
	assertPhysicalSpikeDescriptorEquations(t, implicitFirst, []float64{1, 2, 3})
	assertPhysicalSpikeSignalFlowOracle(t, implicitFirst, []float64{1, 2, 3})

	for name, pair := range map[string][2]*mat.Dense{
		"A": {explicitFirst.A, implicitFirst.A},
		"B": {explicitFirst.B, implicitFirst.B},
		"C": {explicitFirst.C, implicitFirst.C},
		"D": {explicitFirst.D, implicitFirst.D},
		"E": {explicitFirst.E, implicitFirst.E},
	} {
		if !mat.EqualApprox(pair[0], pair[1], 1e-13) {
			t.Fatalf("%s changed when the implicit port moved before its explicit sibling", name)
		}
	}
	if !slices.Equal(explicitFirst.InputName, implicitFirst.InputName) ||
		!slices.Equal(explicitFirst.OutputName, implicitFirst.OutputName) ||
		!slices.Equal(explicitFirst.StateName, implicitFirst.StateName) {
		t.Fatal("channel metadata changed when the implicit port moved before its explicit sibling")
	}
}

func TestPhysicalAssemblySpikeConnectedDelayIsUnsupported(t *testing.T) {
	delayed := physicalSpikeComponent(t, "delayed", 1, explicitPhysicalSpikePorts())
	delayed.System.InputDelay = []float64{0, 0.2}
	plain := physicalSpikeComponent(t, "plain", 2, explicitPhysicalSpikePorts())

	_, err := controlsys.AssemblePhysical(
		"delayed-node",
		[]controlsys.PhysicalComponent{delayed, plain},
		[]controlsys.PhysicalConnection{{
			FromComponent: "delayed", FromPort: "node",
			ToComponent: "plain", ToPort: "node",
		}},
	)
	if !errors.Is(err, controlsys.ErrDescriptorUnsupported) {
		t.Fatalf("connected delayed assembly error = %v, want ErrDescriptorUnsupported", err)
	}
}

func assemblePhysicalSpikeNode(
	t *testing.T,
	name string,
	components []controlsys.PhysicalComponent,
) *controlsys.System {
	t.Helper()
	connections := make([]controlsys.PhysicalConnection, 0, len(components)-1)
	for i := 1; i < len(components); i++ {
		connections = append(connections, controlsys.PhysicalConnection{
			FromComponent: components[i-1].Name,
			FromPort:      "node",
			ToComponent:   components[i].Name,
			ToPort:        "node",
		})
	}
	assembled, err := controlsys.AssemblePhysical(name, components, connections)
	if err != nil {
		t.Fatalf("AssemblePhysical(%s): %v", name, err)
	}
	return assembled
}

func physicalSpikeComponent(
	t *testing.T,
	name string,
	decay float64,
	ports []controlsys.PhysicalPort,
) controlsys.PhysicalComponent {
	t.Helper()
	system, err := controlsys.New(
		mat.NewDense(1, 1, []float64{-decay}),
		mat.NewDense(1, 2, []float64{1, 1}),
		mat.NewDense(2, 1, []float64{1, 1}),
		mat.NewDense(2, 2, nil),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	system.InputName = []string{"load", "node.through"}
	system.OutputName = []string{"position", "node.across"}
	system.StateName = []string{"position"}
	return controlsys.NewPhysicalComponent(name, system, ports)
}

func explicitPhysicalSpikePorts() []controlsys.PhysicalPort {
	return []controlsys.PhysicalPort{{
		Name: "node", Kind: controlsys.PhysicalPortDisplacement, Dimension: 1,
		Input: []int{1}, Output: []int{1},
	}}
}

func assertPhysicalSpikeDescriptorEquations(
	t *testing.T,
	system *controlsys.System,
	decays []float64,
) {
	t.Helper()
	componentCount := len(decays)
	stateCount := 2 * componentCount
	wantE := mat.NewDense(stateCount, stateCount, nil)
	wantA := mat.NewDense(stateCount, stateCount, nil)
	wantB := mat.NewDense(stateCount, componentCount, nil)
	wantC := mat.NewDense(componentCount, stateCount, nil)
	wantD := mat.NewDense(componentCount, componentCount, nil)

	for component, decay := range decays {
		wantE.Set(component, component, 1)
		wantA.Set(component, component, -decay)
		wantA.Set(component, componentCount+component, 1)
		wantB.Set(component, component, 1)
		wantC.Set(component, component, 1)
	}
	for component := 1; component < componentCount; component++ {
		constraint := componentCount + component - 1
		wantA.Set(constraint, 0, -1)
		wantA.Set(constraint, component, 1)
	}
	for component := range componentCount {
		wantA.Set(stateCount-1, componentCount+component, 1)
	}

	if !system.IsDescriptor() {
		t.Fatal("connected physical node did not retain its algebraic constraints")
	}
	for name, pair := range map[string][2]*mat.Dense{
		"A": {system.A, wantA},
		"B": {system.B, wantB},
		"C": {system.C, wantC},
		"D": {system.D, wantD},
		"E": {system.E, wantE},
	} {
		if !mat.EqualApprox(pair[0], pair[1], 1e-13) {
			t.Fatalf("%s does not match the hand-assembled descriptor equation:\ngot:\n%v\nwant:\n%v",
				name, mat.Formatted(pair[0]), mat.Formatted(pair[1]))
		}
	}
}

func assertPhysicalSpikeSignalFlowOracle(
	t *testing.T,
	system *controlsys.System,
	decays []float64,
) {
	t.Helper()
	sumDecay := 0.0
	for _, decay := range decays {
		sumDecay += decay
	}
	wantPole := complex(-sumDecay/float64(len(decays)), 0)
	poles, err := system.Poles()
	if err != nil {
		t.Fatalf("descriptor poles: %v", err)
	}
	if len(poles) != 1 || cmplx.Abs(poles[0]-wantPole) > 1e-11 {
		t.Fatalf("finite descriptor poles = %v, want [%v]", poles, wantPole)
	}

	omega := []float64{0, 0.25, 1, 4}
	response, err := system.FreqResponse(omega)
	if err != nil {
		t.Fatalf("descriptor frequency response: %v", err)
	}
	for sample, frequency := range omega {
		want := 1 / complex(sumDecay, float64(len(decays))*frequency)
		for output := range len(decays) {
			for input := range len(decays) {
				if difference := cmplx.Abs(response.At(sample, output, input) - want); difference > 1e-11 {
					t.Fatalf(
						"H[%d,%d](j%g) differs by %g: got %v want %v",
						output, input, frequency, difference,
						response.At(sample, output, input), want,
					)
				}
			}
		}
	}
}

func assertPhysicalSpikeTimeSimulationBoundary(t *testing.T, system *controlsys.System) {
	t.Helper()
	if _, err := system.ToExplicit(); !errors.Is(err, controlsys.ErrDescriptorSingular) {
		t.Fatalf("ToExplicit error = %v, want ErrDescriptorSingular", err)
	}
	discrete, err := system.DiscretizeZOH(0.1)
	if err != nil {
		return
	}
	if discrete.IsDescriptor() {
		t.Fatal("descriptor discretization is now available; revisit the spike recommendation")
	}
	poles, err := discrete.Poles()
	if err != nil {
		t.Fatalf("poles of the current discretization path: %v", err)
	}
	wantPole := complex(math.Exp(-0.15), 0)
	if len(poles) == 1 && cmplx.Abs(poles[0]-wantPole) <= 1e-11 {
		t.Fatal("physical assembly now has a faithful explicit discretization; revisit the spike recommendation")
	}
}
