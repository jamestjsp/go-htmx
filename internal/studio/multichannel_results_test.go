package studio

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestRectangularSimulationRetainsDeterministicResultChannelIdentity(t *testing.T) {
	inputValues, _ := NewVectorValue([]float64{1, 2})
	inputNames, _ := NewChannelNames([]string{"feed", "recycle"})
	outputNames, _ := NewChannelNames([]string{"temperature", "pressure", "level"})
	gain, _ := NewMatrixValue(3, 2, []float64{
		1, 0,
		0, 1,
		1, -1,
	})
	sourceParameters := defaultParameters(BlockVectorConstant)
	sourceParameters.Vector = &inputValues
	sourceParameters.OutputNames = &inputNames
	gainParameters := defaultParameters(BlockMatrixGain)
	gainParameters.D = &gain
	gainParameters.InputNames = &inputNames
	gainParameters.OutputNames = &outputNames
	scopeParameters := defaultParameters(BlockVectorScope)
	scopeParameters.InputNames = &outputNames

	run, err := simulate(
		[]Block{
			{ID: 1, Kind: BlockVectorConstant, Name: "Inputs", Parameters: sourceParameters},
			{ID: 2, Kind: BlockMatrixGain, Name: "Plant", Parameters: gainParameters},
			{ID: 3, Kind: BlockVectorScope, Name: "Results", Parameters: scopeParameters},
		},
		[]Connection{
			{ID: 1, SourceID: 1, TargetID: 2},
			{ID: 2, SourceID: 2, TargetID: 3},
		},
		SimulationRequest{Duration: 0.2, SampleTime: 0.1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Series) != 3 || len(run.Metrics) != 3 {
		t.Fatalf("result dimensions = %d series, %d metrics", len(run.Series), len(run.Metrics))
	}
	for channel, name := range []string{"temperature", "pressure", "level"} {
		series := run.Series[channel]
		if series.Ref() != (ChannelRef{BlockID: 3, Port: 0, Channel: channel}) ||
			series.ChannelName != name ||
			series.Name != "Results · "+name {
			t.Fatalf("series %d identity = %#v", channel, series.ResultChannel)
		}
		if run.Metrics[channel].ResultChannel != series.ResultChannel {
			t.Fatalf("metric %d identity = %#v, want %#v",
				channel, run.Metrics[channel].ResultChannel, series.ResultChannel)
		}
	}
	for sample := range run.Times {
		if run.Series[0].Values[sample] != 1 ||
			run.Series[1].Values[sample] != 2 ||
			run.Series[2].Values[sample] != -1 {
			t.Fatalf("sample %d = %g, %g, %g",
				sample,
				run.Series[0].Values[sample],
				run.Series[1].Values[sample],
				run.Series[2].Values[sample],
			)
		}
	}
}

func TestLegacyScalarSimulationJSONRemainsReadable(t *testing.T) {
	legacy := `{
		"id":7,
		"duration":1,
		"sampleTime":0.1,
		"times":[0,0.1],
		"series":[{"blockId":4,"name":"Temperature","values":[0,1]}],
		"metrics":[{"name":"Temperature","peak":1,"final":1,"settled":true,"settleTime":0.1}]
	}`
	var run Simulation
	if err := json.Unmarshal([]byte(legacy), &run); err != nil {
		t.Fatal(err)
	}
	if len(run.Series) != 1 ||
		run.Series[0].Ref() != (ChannelRef{BlockID: 4}) ||
		run.Series[0].Name != "Temperature" ||
		len(run.Series[0].Values) != 2 {
		t.Fatalf("legacy series = %#v", run.Series)
	}
	if len(run.Metrics) != 1 || run.Metrics[0].Name != "Temperature" {
		t.Fatalf("legacy metrics = %#v", run.Metrics)
	}
	if _, err := json.Marshal(run); err != nil {
		t.Fatalf("re-encode legacy simulation: %v", err)
	}
}

func TestMultichannelResultIdentityPersistsThroughSimulationJSONStore(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run := Simulation{
		Duration: 1, SampleTime: 0.1, Times: []float64{0, 0.1},
		Series: []Series{
			{
				ResultChannel: ResultChannel{
					BlockID: 8, Port: 0, Channel: 0,
					ChannelName: "temperature", Name: "Results · temperature",
				},
				Values: []float64{0, 1},
			},
			{
				ResultChannel: ResultChannel{
					BlockID: 8, Port: 0, Channel: 1,
					ChannelName: "pressure", Name: "Results · pressure",
				},
				Values: []float64{1, 2},
			},
		},
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.ExecContext(ctx, `
		INSERT INTO simulation_runs(
			flow_id, created_at, duration, sample_time, result_json
		) VALUES(?, ?, ?, ?, ?)`,
		snapshot.Flow.ID, "2099-01-01T00:00:00Z",
		run.Duration, run.SampleTime, string(encoded),
	); err != nil {
		t.Fatal(err)
	}
	reloaded, err := service.snapshot(ctx, snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.LastRun == nil ||
		len(reloaded.LastRun.Series) != 2 ||
		reloaded.LastRun.Series[1].Ref() != (ChannelRef{BlockID: 8, Channel: 1}) ||
		reloaded.LastRun.Series[1].ChannelName != "pressure" {
		t.Fatalf("reloaded run = %#v", reloaded.LastRun)
	}
}

func TestSimulationLimitsCountInclusiveSamplesAndResultChannels(t *testing.T) {
	if err := validateSimulationRequest(
		SimulationRequest{Duration: 49.99, SampleTime: 0.01},
	); err != nil {
		t.Fatalf("exact 5,000-sample run: %v", err)
	}
	if err := validateSimulationRequest(
		SimulationRequest{Duration: 50, SampleTime: 0.01},
	); err == nil || !strings.Contains(err.Error(), "5,000 samples") {
		t.Fatalf("5,001-sample error = %v", err)
	}

	values := make([]float64, maxSimulationResultChannels+1)
	namesText := make([]string, len(values))
	for index := range values {
		values[index] = float64(index)
		namesText[index] = "channel " + formatFloat(float64(index+1))
	}
	vector, _ := NewVectorValue(values)
	names, _ := NewChannelNames(namesText)
	source := defaultParameters(BlockVectorConstant)
	source.Vector = &vector
	source.OutputNames = &names
	scope := defaultParameters(BlockVectorScope)
	scope.InputNames = &names
	_, err := simulate(
		[]Block{
			{ID: 1, Kind: BlockVectorConstant, Name: "Wide source", Parameters: source},
			{ID: 2, Kind: BlockVectorScope, Name: "Wide scope", Parameters: scope},
		},
		[]Connection{{SourceID: 1, TargetID: 2}},
		SimulationRequest{Duration: 0.1, SampleTime: 0.1},
	)
	if err == nil || !strings.Contains(err.Error(), "16 plotted result channels") {
		t.Fatalf("wide result error = %v", err)
	}
}

func TestSpectrumCarriesScalarChannelIdentity(t *testing.T) {
	output := compiledOutput{
		block: Block{ID: 9, Kind: BlockSpectrum, Name: "Vibration spectrum"},
		signal: compiledSignal{
			BlockID: 9, Port: 0, Channel: 0, ChannelName: "acceleration", Width: 1,
		},
	}
	values := make([]float64, 100)
	for sample := range values {
		values[sample] = math.Sin(2 * math.Pi * float64(sample) / 10)
	}
	spectrum := spectrumFor(output, values, 0.1)
	if spectrum.Ref() != (ChannelRef{BlockID: 9}) ||
		spectrum.ChannelName != "acceleration" ||
		spectrum.Name != "Vibration spectrum" {
		t.Fatalf("spectrum identity = %#v", spectrum.ResultChannel)
	}
}
