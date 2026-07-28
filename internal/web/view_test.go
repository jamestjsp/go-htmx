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

func TestEdgePathUsesTheConnectedPortOffsets(t *testing.T) {
	source := studio.Block{
		Kind:     studio.BlockConstant,
		Position: studio.Point{X: 100, Y: 200},
	}
	target := studio.Block{
		Kind:       studio.BlockSum,
		Position:   studio.Point{X: 400, Y: 500},
		Parameters: studio.Parameters{Signs: "+-"},
	}

	got := edgePath(source, 0, target, 1)
	want := "M 272.0 242.0 C 329.6 242.0, 342.4 556.0, 400.0 556.0"
	if got != want {
		t.Fatalf("edgePath() = %q, want %q", got, want)
	}
}
