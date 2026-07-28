package studio

import (
	"context"
	"errors"
	"math/cmplx"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestBuildControlModelsMatchesIndependentSISOOracle(t *testing.T) {
	snapshot, spec := sisoControlFixture()
	resolved, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, spec,
	)
	if err != nil {
		t.Fatal(err)
	}
	models, err := buildControlModels(
		snapshot, resolved, ControlModelBuildRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, system := range map[string]*controlsys.System{
		"plant":             models.Plant,
		"controller":        models.Controller,
		"generalized plant": models.GeneralizedPlant,
		"estimator plant":   models.EstimatorPlant,
	} {
		if _, inputs, outputs := system.Dims(); inputs != 1 || outputs != 1 {
			t.Fatalf("%s dimensions = %d×%d, want 1×1", name, outputs, inputs)
		}
	}
	if len(models.Points) != 2 {
		t.Fatalf("analysis point count = %d, want 2", len(models.Points))
	}

	omega := []float64{0.2, 1, 4}
	plantResponse, err := models.Plant.FreqResponse(omega)
	if err != nil {
		t.Fatal(err)
	}
	controllerResponse, err := models.Controller.FreqResponse(omega)
	if err != nil {
		t.Fatal(err)
	}
	for i, frequency := range omega {
		wantPlant := 1 / complex(1, frequency)
		if diff := cmplx.Abs(plantResponse.At(i, 0, 0) - wantPlant); diff > 1e-12 {
			t.Fatalf("plant at %g = %v, want %v", frequency, plantResponse.At(i, 0, 0), wantPlant)
		}
		if got := controllerResponse.At(i, 0, 0); cmplx.Abs(got-2) > 1e-12 {
			t.Fatalf("controller at %g = %v, want 2", frequency, got)
		}
		openResponse, err := models.Points[0].OpenLoop.FreqResponse([]float64{frequency})
		if err != nil {
			t.Fatal(err)
		}
		wantOpen := 2 * wantPlant
		if diff := cmplx.Abs(openResponse.At(0, 0, 0) - wantOpen); diff > 1e-12 {
			t.Fatalf("open loop at %g = %v, want %v", frequency, openResponse.At(0, 0, 0), wantOpen)
		}
		closedResponse, err := models.Points[0].ClosedLoop.FreqResponse([]float64{frequency})
		if err != nil {
			t.Fatal(err)
		}
		wantClosed := wantOpen / (1 + wantOpen)
		if diff := cmplx.Abs(closedResponse.At(0, 0, 0) - wantClosed); diff > 1e-11 {
			t.Fatalf("closed loop at %g = %v, want %v", frequency, closedResponse.At(0, 0, 0), wantClosed)
		}
	}
}

func TestBuildControlModelsPreservesNamedMIMOOrdering(t *testing.T) {
	plantMatrix, _ := NewMatrixValue(2, 2, []float64{
		1, 2,
		0, 1,
	})
	controllerMatrix, _ := NewMatrixValue(2, 2, []float64{
		1, 0,
		3, 1,
	})
	uNames, _ := NewChannelNames([]string{"u1", "u2"})
	yNames, _ := NewChannelNames([]string{"y1", "y2"})
	snapshot := Snapshot{
		Flow: Flow{ID: 9},
		Blocks: []Block{
			{
				ID: 1, FlowID: 9, Kind: BlockMatrixGain, Name: "Plant",
				Parameters: Parameters{
					D: &plantMatrix, InputNames: &uNames, OutputNames: &yNames,
				},
			},
			{
				ID: 2, FlowID: 9, Kind: BlockMatrixGain, Name: "Controller",
				Parameters: Parameters{
					D: &controllerMatrix, InputNames: &yNames, OutputNames: &uNames,
				},
			},
		},
		Connections: []Connection{
			{FlowID: 9, SourceID: 2, TargetID: 1},
			{FlowID: 9, SourceID: 1, TargetID: 2},
		},
	}
	plantInputs := namedRefs(1, ChannelInput, []string{"u1", "u2"})
	plantOutputs := namedRefs(1, ChannelOutput, []string{"y1", "y2"})
	controllerInputs := namedRefs(2, ChannelInput, []string{"y1", "y2"})
	controllerOutputs := namedRefs(2, ChannelOutput, []string{"u1", "u2"})
	spec := ControlRoleSpec{
		Version: controlRoleSpecVersion,
		Plant: PlantRole{
			Blocks:             []int64{1},
			ControlInputs:      plantInputs,
			MeasurementOutputs: plantOutputs,
		},
		Controller: ControllerRole{
			Blocks:            []int64{2},
			MeasurementInputs: controllerInputs,
			ControlOutputs:    controllerOutputs,
		},
		AnalysisPoints: []AnalysisPointRole{
			{
				Name: "actuator", Location: AnalysisPointPlantInput,
				Pairs: loopPairs(controllerOutputs, plantInputs),
			},
			{
				Name: "sensor", Location: AnalysisPointPlantOutput,
				Pairs: loopPairs(plantOutputs, controllerInputs),
			},
		},
	}
	resolved, err := resolveControlRoleSpec(snapshot.Blocks, snapshot.Connections, spec)
	if err != nil {
		t.Fatal(err)
	}
	models, err := buildControlModels(snapshot, resolved, ControlModelBuildRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := models.Plant.InputName; !equalStrings(got, []string{"u1", "u2"}) {
		t.Fatalf("plant input names = %v", got)
	}
	if got := models.Plant.OutputName; !equalStrings(got, []string{"y1", "y2"}) {
		t.Fatalf("plant output names = %v", got)
	}
	if got := models.Controller.InputName; !equalStrings(got, []string{"y1", "y2"}) {
		t.Fatalf("controller input names = %v", got)
	}
	if len(models.Points) != 2 {
		t.Fatalf("analysis points = %#v", models.Points)
	}
	wantPlantOutputLoop := mat.NewDense(2, 2, []float64{
		7, 2,
		3, 1,
	})
	if !mat.EqualApprox(models.Points[1].OpenLoop.D, wantPlantOutputLoop, 1e-14) {
		t.Fatalf(
			"plant-output loop =\n%v\nwant\n%v",
			mat.Formatted(models.Points[1].OpenLoop.D),
			mat.Formatted(wantPlantOutputLoop),
		)
	}
}

func TestControlRolesRoundTripDuplicateAndReferencedDelete(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control-roles.db")
	service := openTestStudio(t, path)
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFlow(ctx, current.Project.ID, "Control roles")
	if err != nil {
		t.Fatal(err)
	}
	flowID := created.Snapshot.Flow.ID
	_, plantID, err := service.AddBlock(ctx, flowID, BlockLag, Point{X: 100, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, controllerID, err := service.AddBlock(ctx, flowID, BlockGain, Point{X: 400, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateBlock(ctx, controllerID, BlockUpdate{
		Name: "Controller",
		Parameters: map[string]string{
			"gain": "2",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, flowID, Wire{
		SourceID: controllerID, TargetID: plantID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, flowID, Wire{
		SourceID: plantID, TargetID: controllerID,
	}); err != nil {
		t.Fatal(err)
	}
	spec := sisoRoleSpec(plantID, controllerID)
	stored, err := service.AssignControlRoles(ctx, flowID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version != controlRoleSpecVersion {
		t.Fatalf("stored version = %d", stored.Version)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}

	service, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	roundTrip, err := service.ControlRoles(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Plant.Blocks[0] != plantID ||
		roundTrip.Controller.Blocks[0] != controllerID {
		t.Fatalf("round-tripped roles = %#v", roundTrip)
	}
	originalModels, err := service.BuildControlModels(
		ctx, flowID, ControlModelBuildRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	duplicated, err := service.DuplicateFlow(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	copiedSpec, err := service.ControlRoles(ctx, duplicated.Snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copiedSpec.Plant.Blocks[0] == plantID ||
		copiedSpec.Controller.Blocks[0] == controllerID {
		t.Fatalf("copied roles retained source block ids: %#v", copiedSpec)
	}
	copiedModels, err := service.BuildControlModels(
		ctx, duplicated.Snapshot.Flow.ID, ControlModelBuildRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !mat.EqualApprox(originalModels.Plant.A, copiedModels.Plant.A, 1e-14) ||
		!mat.EqualApprox(originalModels.Controller.D, copiedModels.Controller.D, 1e-14) {
		t.Fatal("copied control models changed numerically")
	}

	_, unrelatedID, err := service.AddBlock(
		ctx, flowID, BlockGain, Point{X: 700, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteBlock(ctx, unrelatedID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ControlRoles(ctx, flowID); err != nil {
		t.Fatalf("unrelated delete cleared roles: %v", err)
	}
	if _, err := service.DeleteBlock(ctx, plantID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ControlRoles(ctx, flowID); err == nil ||
		!strings.Contains(err.Error(), "assign plant and controller roles") {
		t.Fatalf("roles after referenced delete error = %v", err)
	}
}

func TestAssignControlRolesDoesNotInvalidateOrAlterSimulation(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	before, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Run(ctx, before.Flow.ID, SimulationRequest{
		Duration: 2, SampleTime: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var controller, plant Block
	for _, block := range before.Blocks {
		switch block.Name {
		case "Valve gain":
			controller = block
		case "Reactor":
			plant = block
		}
	}
	if controller.ID == 0 || plant.ID == 0 {
		t.Fatalf("seeded control blocks not found: %#v", before.Blocks)
	}
	spec := sisoRoleSpec(plant.ID, controller.ID)
	spec.AnalysisPoints = spec.AnalysisPoints[:1]
	if _, err := service.AssignControlRoles(ctx, before.Flow.ID, spec); err != nil {
		t.Fatal(err)
	}
	after, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Flow.ModelUpdatedAt != before.Flow.ModelUpdatedAt {
		t.Fatalf(
			"role assignment changed model revision from %v to %v",
			before.Flow.ModelUpdatedAt, after.Flow.ModelUpdatedAt,
		)
	}
	if run.LastRun == nil {
		t.Fatal("seeded simulation was not persisted")
	}
	if after.LastRun == nil || after.LastRun.ID != run.LastRun.ID {
		t.Fatalf("role assignment invalidated simulation: %#v", after.LastRun)
	}
	if got, want := after.LastRun.Series[0].Values, run.LastRun.Series[0].Values; len(got) != len(want) {
		t.Fatalf("simulation samples changed from %d to %d", len(want), len(got))
	} else {
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("simulation sample %d changed from %g to %g", i, want[i], got[i])
			}
		}
	}
	if _, err := service.BuildControlModels(
		ctx, before.Flow.ID, ControlModelBuildRequest{},
	); err != nil {
		t.Fatal(err)
	}
}

func TestControlRoleValidationDiagnosesStaleAndMismatchedAssignments(t *testing.T) {
	snapshot, spec := sisoControlFixture()

	stale := cloneControlRoleSpec(spec)
	stale.Plant.MeasurementOutputs[0].ChannelName = "renamed"
	if _, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, stale,
	); err == nil || !strings.Contains(err.Error(), "no longer has named channel") {
		t.Fatalf("stale channel error = %v", err)
	}

	duplicate := cloneControlRoleSpec(spec)
	duplicate.Plant.ControlInputs = append(
		duplicate.Plant.ControlInputs, duplicate.Plant.ControlInputs[0],
	)
	if _, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, duplicate,
	); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate role error = %v", err)
	}

	missing := cloneControlRoleSpec(spec)
	missing.Plant.Blocks[0] = 999
	if _, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, missing,
	); err == nil || !strings.Contains(err.Error(), "missing block 999") {
		t.Fatalf("missing block error = %v", err)
	}

	mismatch := cloneControlRoleSpec(spec)
	mismatch.Controller.ControlOutputs = nil
	if _, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, mismatch,
	); err == nil || !strings.Contains(err.Error(), "control inputs") {
		t.Fatalf("dimension mismatch error = %v", err)
	}

	wrongDirection := cloneControlRoleSpec(spec)
	wrongDirection.Controller.MeasurementInputs[0].Direction = ChannelOutput
	if _, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, wrongDirection,
	); err == nil || !strings.Contains(err.Error(), "must reference a input channel") {
		t.Fatalf("direction error = %v", err)
	}
}

func TestBuildControlModelsRejectsMixedDomains(t *testing.T) {
	snapshot, spec := sisoControlFixture()
	snapshot.Blocks[1].Kind = BlockUnitDelay
	snapshot.Blocks[1].Parameters = defaultParameters(BlockUnitDelay)
	resolved, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, spec,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildControlModels(
		snapshot, resolved, ControlModelBuildRequest{},
	); err == nil || !strings.Contains(err.Error(), "different time domains") {
		t.Fatalf("mixed-domain error = %v", err)
	}
}

func TestBuildControlModelsLetsNeutralControllerInheritDiscretePlantRate(t *testing.T) {
	snapshot, spec := sisoControlFixture()
	snapshot.Blocks[0].Kind = BlockUnitDelay
	snapshot.Blocks[0].Parameters = defaultParameters(BlockUnitDelay)
	resolved, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, spec,
	)
	if err != nil {
		t.Fatal(err)
	}
	models, err := buildControlModels(
		snapshot, resolved, ControlModelBuildRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if models.Plant.Dt != 0.1 || models.Controller.Dt != 0.1 {
		t.Fatalf(
			"discrete rates = plant %g, controller %g",
			models.Plant.Dt, models.Controller.Dt,
		)
	}
	for _, point := range models.Points {
		if point.OpenLoop.Dt != 0.1 || point.ClosedLoop.Dt != 0.1 {
			t.Fatalf(
				"%s rates = open %g, closed %g",
				point.Name, point.OpenLoop.Dt, point.ClosedLoop.Dt,
			)
		}
	}
}

func TestBuildControlModelsPreservesExactDelayMetadata(t *testing.T) {
	snapshot, _ := sisoControlFixture()
	snapshot.Blocks[0].Kind = BlockDelay
	snapshot.Blocks[0].Parameters = defaultParameters(BlockDelay)
	snapshot.Blocks[0].Parameters.Delay = 0.75
	snapshot.Blocks[0].Parameters.DelayMode = delayModeExact
	snapshot.Blocks = append(snapshot.Blocks, Block{
		ID: 3, FlowID: 1, Kind: BlockLag, Name: "Plant dynamics",
		Parameters: Parameters{TimeConstant: 1},
	})
	snapshot.Connections = []Connection{
		{FlowID: 1, SourceID: 2, TargetID: 1},
		{FlowID: 1, SourceID: 1, TargetID: 3},
		{FlowID: 1, SourceID: 3, TargetID: 2},
	}
	spec := sisoRoleSpec(1, 2)
	spec.Plant.Blocks = []int64{1, 3}
	plantOutput := NamedChannelRef{
		BlockID: 3, Direction: ChannelOutput, ChannelName: "value",
	}
	spec.Plant.MeasurementOutputs = []NamedChannelRef{plantOutput}
	spec.AnalysisPoints[1].Pairs[0].Output = plantOutput
	resolved, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, spec,
	)
	if err != nil {
		t.Fatal(err)
	}
	models, err := buildControlModels(
		snapshot, resolved, ControlModelBuildRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !models.Plant.HasDelay() && !models.Plant.HasInternalDelay() {
		t.Fatal("plant lost its exact delay metadata")
	}
	if !models.Points[0].ClosedLoop.HasInternalDelay() {
		t.Fatal("closed loop did not promote exact delay to controlsys internal LFT")
	}
}

func TestPureDelayControlModelReturnsErrorInsteadOfPanicking(t *testing.T) {
	snapshot, spec := sisoControlFixture()
	snapshot.Blocks[0].Kind = BlockDelay
	snapshot.Blocks[0].Parameters = defaultParameters(BlockDelay)
	snapshot.Blocks[0].Parameters.Delay = 0.5
	snapshot.Blocks[0].Parameters.DelayMode = delayModeExact
	resolved, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, spec,
	)
	if err != nil {
		t.Fatal(err)
	}
	models, err := buildControlModels(
		snapshot, resolved, ControlModelBuildRequest{},
	)
	if err != nil && !strings.Contains(err.Error(), "controlsys failed") {
		t.Fatalf("pure-delay closed-loop error = %v", err)
	}
	if err == nil && !models.Plant.HasDelay() && !models.Plant.HasInternalDelay() {
		t.Fatal("successful pure-delay model lost delay metadata")
	}
}

func TestNamedRoleResolutionSurvivesConsistentMIMOReorder(t *testing.T) {
	plantMatrix, _ := NewMatrixValue(2, 2, []float64{
		1, 2,
		0, 1,
	})
	controllerMatrix, _ := NewMatrixValue(2, 2, []float64{
		1, 0,
		3, 1,
	})
	reorderedU, _ := NewChannelNames([]string{"u2", "u1"})
	reorderedY, _ := NewChannelNames([]string{"y2", "y1"})
	snapshot := Snapshot{
		Flow: Flow{ID: 12},
		Blocks: []Block{
			{
				ID: 1, FlowID: 12, Kind: BlockMatrixGain, Name: "Plant",
				Parameters: Parameters{
					D: &plantMatrix, InputNames: &reorderedU, OutputNames: &reorderedY,
				},
			},
			{
				ID: 2, FlowID: 12, Kind: BlockMatrixGain, Name: "Controller",
				Parameters: Parameters{
					D: &controllerMatrix, InputNames: &reorderedY, OutputNames: &reorderedU,
				},
			},
		},
		Connections: []Connection{
			{FlowID: 12, SourceID: 2, TargetID: 1},
			{FlowID: 12, SourceID: 1, TargetID: 2},
		},
	}
	plantInputs := namedRefs(1, ChannelInput, []string{"u1", "u2"})
	plantOutputs := namedRefs(1, ChannelOutput, []string{"y1", "y2"})
	controllerInputs := namedRefs(2, ChannelInput, []string{"y1", "y2"})
	controllerOutputs := namedRefs(2, ChannelOutput, []string{"u1", "u2"})
	spec := ControlRoleSpec{
		Version: controlRoleSpecVersion,
		Plant: PlantRole{
			Blocks:             []int64{1},
			ControlInputs:      plantInputs,
			MeasurementOutputs: plantOutputs,
		},
		Controller: ControllerRole{
			Blocks:            []int64{2},
			MeasurementInputs: controllerInputs,
			ControlOutputs:    controllerOutputs,
		},
		AnalysisPoints: []AnalysisPointRole{
			{
				Name: "actuator", Location: AnalysisPointPlantInput,
				Pairs: loopPairs(controllerOutputs, plantInputs),
			},
			{
				Name: "sensor", Location: AnalysisPointPlantOutput,
				Pairs: loopPairs(plantOutputs, controllerInputs),
			},
		},
	}
	resolved, err := resolveControlRoleSpec(snapshot.Blocks, snapshot.Connections, spec)
	if err != nil {
		t.Fatal(err)
	}
	models, err := buildControlModels(snapshot, resolved, ControlModelBuildRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(models.Plant.InputName, []string{"u1", "u2"}) ||
		!equalStrings(models.Plant.OutputName, []string{"y1", "y2"}) {
		t.Fatalf(
			"resolved names = inputs %v outputs %v",
			models.Plant.InputName, models.Plant.OutputName,
		)
	}
	wantReorderedPlant := mat.NewDense(2, 2, []float64{
		1, 0,
		2, 1,
	})
	if !mat.EqualApprox(models.Plant.D, wantReorderedPlant, 1e-14) {
		t.Fatalf(
			"reordered plant =\n%v\nwant\n%v",
			mat.Formatted(models.Plant.D), mat.Formatted(wantReorderedPlant),
		)
	}
}

func TestFreshDatabaseHasNoInferredControlRoles(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "fresh-control.db"))
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ControlRoles(ctx, snapshot.Flow.ID); err == nil ||
		!strings.Contains(err.Error(), "assign plant and controller roles") {
		t.Fatalf("fresh control roles error = %v", err)
	}
	var table string
	if err := service.db.QueryRowContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name = 'control_model_specs'`,
	).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table != "control_model_specs" {
		t.Fatalf("control model table = %q", table)
	}
}

func TestLegacyDatabaseAddsEmptyControlRoleStorage(t *testing.T) {
	ctx := context.Background()
	service, err := Open(ctx, openLegacyPortsDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	var rows int
	if err := service.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM control_model_specs",
	).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("legacy migration inferred %d control role rows", rows)
	}
}

func TestControlRoleStorageRejectsCorruptAndMismatchedVersions(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.ExecContext(ctx, `
		INSERT INTO control_model_specs(flow_id, version, spec_json)
		VALUES(?, 1, '{')`, snapshot.Flow.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ControlRoles(ctx, snapshot.Flow.ID); err == nil ||
		!strings.Contains(err.Error(), "decode control roles") {
		t.Fatalf("corrupt JSON error = %v", err)
	}
	if _, err := service.db.ExecContext(ctx, `
		UPDATE control_model_specs
		SET version = 2, spec_json = '{"version":1}'
		WHERE flow_id = ?`, snapshot.Flow.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ControlRoles(ctx, snapshot.Flow.ID); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("version mismatch error = %v", err)
	}
}

func sisoControlFixture() (Snapshot, ControlRoleSpec) {
	plant := Block{
		ID: 1, FlowID: 1, Kind: BlockLag, Name: "Plant",
		Parameters: Parameters{TimeConstant: 1},
	}
	controller := Block{
		ID: 2, FlowID: 1, Kind: BlockGain, Name: "Controller",
		Parameters: Parameters{Gain: 2},
	}
	return Snapshot{
		Flow:   Flow{ID: 1},
		Blocks: []Block{plant, controller},
		Connections: []Connection{
			{FlowID: 1, SourceID: 2, TargetID: 1},
			{FlowID: 1, SourceID: 1, TargetID: 2},
		},
	}, sisoRoleSpec(1, 2)
}

func sisoRoleSpec(plantID, controllerID int64) ControlRoleSpec {
	plantInput := NamedChannelRef{
		BlockID: plantID, Direction: ChannelInput, ChannelName: "value",
	}
	plantOutput := NamedChannelRef{
		BlockID: plantID, Direction: ChannelOutput, ChannelName: "value",
	}
	controllerInput := NamedChannelRef{
		BlockID: controllerID, Direction: ChannelInput, ChannelName: "value",
	}
	controllerOutput := NamedChannelRef{
		BlockID: controllerID, Direction: ChannelOutput, ChannelName: "value",
	}
	return ControlRoleSpec{
		Version: controlRoleSpecVersion,
		Plant: PlantRole{
			Blocks:             []int64{plantID},
			ControlInputs:      []NamedChannelRef{plantInput},
			MeasurementOutputs: []NamedChannelRef{plantOutput},
		},
		Controller: ControllerRole{
			Blocks:            []int64{controllerID},
			MeasurementInputs: []NamedChannelRef{controllerInput},
			ControlOutputs:    []NamedChannelRef{controllerOutput},
		},
		AnalysisPoints: []AnalysisPointRole{
			{
				Name: "actuator", Location: AnalysisPointPlantInput,
				Pairs: []LoopBreakPair{{
					Output: controllerOutput, Input: plantInput,
				}},
			},
			{
				Name: "sensor", Location: AnalysisPointPlantOutput,
				Pairs: []LoopBreakPair{{
					Output: plantOutput, Input: controllerInput,
				}},
			},
		},
	}
}

func namedRefs(
	blockID int64,
	direction ChannelDirection,
	names []string,
) []NamedChannelRef {
	refs := make([]NamedChannelRef, len(names))
	for i, name := range names {
		refs[i] = NamedChannelRef{
			BlockID: blockID, Direction: direction, ChannelName: name,
		}
	}
	return refs
}

func loopPairs(outputs, inputs []NamedChannelRef) []LoopBreakPair {
	pairs := make([]LoopBreakPair, len(outputs))
	for i := range outputs {
		pairs[i] = LoopBreakPair{Output: outputs[i], Input: inputs[i]}
	}
	return pairs
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestControlRolesMissingFlowIsNotFound(t *testing.T) {
	service := openTestStudio(t, ":memory:")
	if _, err := service.AssignControlRoles(
		context.Background(), 9999, ControlRoleSpec{},
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing flow error = %v", err)
	}
}
