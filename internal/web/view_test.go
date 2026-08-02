package web

import (
	"testing"

	"github.com/jamestjsp/go-htmx/internal/studio"
)

func TestPortCenterOffsetDistributesPortsAlongTheBlockEdge(t *testing.T) {
	tests := []struct {
		count int
		index int
		want  int
	}{
		{count: 1, index: 0, want: 42},
		{count: 2, index: 0, want: 28},
		{count: 2, index: 1, want: 56},
		{count: 3, index: 0, want: 21},
		{count: 3, index: 1, want: 42},
		{count: 3, index: 2, want: 63},
		{count: 0, index: 0, want: studio.BlockHeight / 2},
		{count: 2, index: -1, want: studio.BlockHeight / 2},
		{count: 1, index: 9, want: studio.BlockHeight / 2},
	}
	for _, test := range tests {
		if got := portCenterOffset(test.count, test.index); got != test.want {
			t.Errorf("portCenterOffset(%d, %d) = %d, want %d", test.count, test.index, got, test.want)
		}
	}
}

func TestPortViewsPreserveTheSinglePortPositionAndBoundDenseHitAreas(t *testing.T) {
	gain := inputPortViews(studio.Block{Kind: studio.BlockGain})
	if len(gain) != 1 || gain[0].Top != 34 || gain[0].HitHeight != studio.BlockHeight {
		t.Fatalf("single input port = %+v", gain)
	}

	sum := inputPortViews(studio.Block{
		Kind:       studio.BlockSum,
		Parameters: studio.Parameters{Signs: "++++++++++++++++"},
	})
	if len(sum) != 16 {
		t.Fatalf("sum input ports = %d, want 16", len(sum))
	}
	if sum[0].HitHeight != 4 {
		t.Fatalf("dense port hit height = %d, want 4", sum[0].HitHeight)
	}
	if sum[0].Size != 4 {
		t.Fatalf("dense port size = %d, want 4", sum[0].Size)
	}
	for index := 1; index < len(sum); index++ {
		if sum[index].Top < sum[index-1].Top+sum[index-1].Size {
			t.Fatalf("ports %d and %d overlap: %+v, %+v", index-1, index, sum[index-1], sum[index])
		}
	}
}

func TestFidelityViewNamesExactAndApproximateExecutionChoices(t *testing.T) {
	view := newFidelityView(studio.Fidelity{
		BaseStep:     0.1,
		ModelDomain:  "discrete",
		Driver:       "per-sample-simulate",
		SourceHold:   "sampled-zero-order-hold",
		SegmentCount: 2,
		BlockRates: []studio.BlockRate{{
			BlockName: "Controller", Mode: "inherited",
			SampleTime: 0.1, UpdateEvery: 1,
		}},
		Delays: []studio.DelayProvenance{
			{
				BlockName: "Pipe", Representation: "exact",
				Delay: 0.2, SampleTime: 0.1, Aligned: true,
			},
			{
				BlockName: "Sensor", Representation: "thiran",
				Delay: 0.35, ApproximationOrder: 3, SampleTime: 0.1,
			},
		},
	}, 0.2)

	if view.Driver != "Stateful discrete · Simulate" ||
		view.BaseStep != "0.1 s" ||
		view.SourceHold != "sampled zero order hold" ||
		view.Segments != 2 {
		t.Fatalf("fidelity view = %#v", view)
	}
	for _, expected := range []string{
		"Pipe · exact 0.2 s · aligned at 0.1 s",
		"Sensor · Thiran 3 · 0.35 s at 0.1 s",
		"Controller · 0.1 s · inherited",
	} {
		found := false
		for _, text := range append(append([]string(nil), view.Delays...), view.Rates...) {
			if text == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("fidelity view does not contain %q: %#v", expected, view)
		}
	}
}

func TestFidelityViewFillsLegacyRunDefaults(t *testing.T) {
	view := newFidelityView(studio.Fidelity{}, 0.2)
	if view.Driver != "Batch LTI · Lsim" ||
		view.Domain != "continuous" ||
		view.BaseStep != "0.2 s" ||
		view.Segments != 1 {
		t.Fatalf("legacy fidelity view = %#v", view)
	}
}
