package web

import (
	"strings"
	"testing"
	"time"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestFrequencyResultViewRendersEveryNamedMIMOTrace(t *testing.T) {
	value := func(numbers ...float64) []*float64 {
		result := make([]*float64, len(numbers))
		for index := range numbers {
			number := numbers[index]
			result[index] = &number
		}
		return result
	}
	record := studio.FrequencyAnalysisRecord{
		CreatedAt: time.Date(2026, 7, 28, 1, 2, 3, 0, time.UTC),
		Result: studio.FrequencyAnalysis{
			ModelUpdatedAt: time.Date(2026, 7, 28, 1, 0, 0, 0, time.UTC),
			Inputs: []studio.AnalyzedChannel{
				{ChannelRef: studio.ChannelRef{BlockID: 1, Channel: 0}, Name: "feed"},
				{ChannelRef: studio.ChannelRef{BlockID: 1, Channel: 1}, Name: "recycle"},
			},
			Outputs: []studio.AnalyzedChannel{
				{ChannelRef: studio.ChannelRef{BlockID: 2, Channel: 0}, Name: "temperature"},
				{ChannelRef: studio.ChannelRef{BlockID: 2, Channel: 1}, Name: "pressure"},
			},
			Grid: studio.FrequencyGrid{Omega: []float64{0.1, 1, 10}},
			Units: studio.FrequencyUnits{
				Frequency: "rad/s", Magnitude: "dB", Phase: "degrees",
			},
			Bode: []studio.BodeTrace{
				{InputIndex: 0, OutputIndex: 0, MagnitudeDB: value(1, 2, 3), PhaseDegrees: value(-1, -2, -3)},
				{InputIndex: 1, OutputIndex: 0, MagnitudeDB: value(2, 3, 4), PhaseDegrees: value(-2, -3, -4)},
				{InputIndex: 0, OutputIndex: 1, MagnitudeDB: value(3, 4, 5), PhaseDegrees: value(-3, -4, -5)},
				{InputIndex: 1, OutputIndex: 1, MagnitudeDB: value(4, 5, 6), PhaseDegrees: value(-4, -5, -6)},
			},
		},
	}

	view := frequencyResultView(record)
	if view.Channel != "2 named inputs → 2 named outputs" || len(view.Plots) != 2 {
		t.Fatalf("frequency view = %#v", view)
	}
	for _, plot := range view.Plots {
		if len(plot.Paths) != 4 {
			t.Fatalf("%s paths = %d, want 4", plot.Title, len(plot.Paths))
		}
		keys := make(map[string]struct{})
		for _, path := range plot.Paths {
			if !strings.Contains(path.Name, " ← ") || path.Key == "" {
				t.Fatalf("%s path = %#v", plot.Title, path)
			}
			keys[path.Key] = struct{}{}
		}
		if len(keys) != 4 {
			t.Fatalf("%s unique keys = %d", plot.Title, len(keys))
		}
	}
	for index := range 4 {
		if view.Plots[0].Paths[index].Key != view.Plots[1].Paths[index].Key {
			t.Fatalf("magnitude/phase key %d differs", index)
		}
	}
}

func TestChartViewUsesStableResultChannelKeysAndLabels(t *testing.T) {
	run := &studio.Simulation{
		Duration:   1,
		SampleTime: 0.1,
		Times:      []float64{0, 1},
		Series: []studio.Series{
			{
				ResultChannel: studio.ResultChannel{
					BlockID: 7, Port: 0, Channel: 0,
					ChannelName: "temperature", Name: "Results · temperature",
				},
				Values: []float64{0, 1},
			},
			{
				ResultChannel: studio.ResultChannel{
					BlockID: 7, Port: 0, Channel: 1,
					ChannelName: "pressure", Name: "Results · pressure",
				},
				Values: []float64{1, 2},
			},
		},
	}
	view := newChartView(run)
	if len(view.Paths) != 2 ||
		view.Paths[0].Key != "7:0:0" ||
		view.Paths[1].Key != "7:0:1" ||
		view.Paths[0].Name != "Results · temperature" {
		t.Fatalf("chart paths = %#v", view.Paths)
	}
}
