package web

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jamestjsp/go-htmx/internal/studio"
)

type pageView struct {
	Workbench workbenchView
}

type workbenchView struct {
	Snapshot        studio.Snapshot
	Blocks          []blockView
	Connections     []connectionView
	Selected        *blockView
	SelectedLinks   []inspectorLink
	Palette         []paletteItem
	Chart           chartView
	Error           string
	Updated         string
	BlockCount      int
	ConnectionCount int
}

type blockView struct {
	studio.Block
	Selected      bool
	ParameterText string
}

type connectionView struct {
	studio.Connection
	Path       string
	SourceName string
	TargetName string
}

type inspectorLink struct {
	ID        int64
	Direction string
	OtherName string
}

type paletteItem struct {
	Kind        studio.BlockKind
	Label       string
	Description string
	Glyph       string
	X           int
	Y           int
}

type chartView struct {
	Present    bool
	Paths      []chartPath
	YGrid      []chartGrid
	XGrid      []chartGrid
	Duration   string
	SampleTime string
	CreatedAt  string
	Metrics    []studio.Metric
}

type chartPath struct {
	Name  string
	D     string
	Color string
}

type chartGrid struct {
	Position float64
	Label    string
}

func newWorkbenchView(snapshot studio.Snapshot, selectedID int64, errorMessage string) workbenchView {
	view := workbenchView{
		Snapshot:        snapshot,
		Error:           errorMessage,
		Updated:         relativeTime(snapshot.Flow.UpdatedAt),
		BlockCount:      len(snapshot.Blocks),
		ConnectionCount: len(snapshot.Connections),
		Palette: []paletteItem{
			{studio.BlockSource, "Source", "Step input", "↗", 30, 90},
			{studio.BlockGain, "Gain", "Scale a signal", "×", 30, 90},
			{studio.BlockLag, "Lag", "First-order dynamics", "τ", 30, 90},
			{studio.BlockSum, "Sum", "Merge signals", "Σ", 30, 90},
			{studio.BlockScope, "Scope", "Plot an output", "⌁", 30, 90},
		},
	}

	blockNames := make(map[int64]string, len(snapshot.Blocks))
	for _, block := range snapshot.Blocks {
		blockNames[block.ID] = block.Name
		item := blockView{
			Block:    block,
			Selected: block.ID == selectedID,
		}
		switch block.Kind {
		case studio.BlockSource:
			item.ParameterText = fmt.Sprintf("%.2g step", block.Parameters.Amplitude)
		case studio.BlockGain:
			item.ParameterText = fmt.Sprintf("K = %.3g", block.Parameters.Gain)
		case studio.BlockLag:
			item.ParameterText = fmt.Sprintf("τ = %.3g s", block.Parameters.TimeConstant)
		case studio.BlockSum:
			item.ParameterText = "multi-input"
		case studio.BlockScope:
			item.ParameterText = "trend output"
		}
		view.Blocks = append(view.Blocks, item)
		if item.Selected {
			copy := item
			view.Selected = &copy
		}
	}

	for _, connection := range snapshot.Connections {
		source := blockByID(snapshot.Blocks, connection.SourceID)
		target := blockByID(snapshot.Blocks, connection.TargetID)
		view.Connections = append(view.Connections, connectionView{
			Connection: connection,
			Path:       edgePath(source.Position, target.Position),
			SourceName: blockNames[connection.SourceID],
			TargetName: blockNames[connection.TargetID],
		})
		if connection.SourceID == selectedID {
			view.SelectedLinks = append(view.SelectedLinks, inspectorLink{
				ID: connection.ID, Direction: "to", OtherName: blockNames[connection.TargetID],
			})
		}
		if connection.TargetID == selectedID {
			view.SelectedLinks = append(view.SelectedLinks, inspectorLink{
				ID: connection.ID, Direction: "from", OtherName: blockNames[connection.SourceID],
			})
		}
	}
	view.Chart = newChartView(snapshot.LastRun)
	return view
}

func blockByID(blocks []studio.Block, id int64) studio.Block {
	for _, block := range blocks {
		if block.ID == id {
			return block
		}
	}
	return studio.Block{}
}

func edgePath(source, target studio.Point) string {
	startX := float64(source.X + 172)
	startY := float64(source.Y + 42)
	endX := float64(target.X)
	endY := float64(target.Y + 42)
	distance := math.Abs(endX - startX)
	bend := math.Max(54, distance*0.45)
	return fmt.Sprintf("M %.1f %.1f C %.1f %.1f, %.1f %.1f, %.1f %.1f",
		startX, startY, startX+bend, startY, endX-bend, endY, endX, endY)
}

func newChartView(run *studio.Simulation) chartView {
	if run == nil || len(run.Times) == 0 || len(run.Series) == 0 {
		return chartView{}
	}
	const (
		width  = 780.0
		height = 228.0
		left   = 48.0
		right  = 18.0
		top    = 18.0
		bottom = 32.0
	)
	minY, maxY := 0.0, 0.0
	for _, series := range run.Series {
		for _, value := range series.Values {
			minY = math.Min(minY, value)
			maxY = math.Max(maxY, value)
		}
	}
	if maxY-minY < 1e-9 {
		maxY++
		minY--
	}
	padding := (maxY - minY) * 0.12
	maxY += padding
	minY -= padding
	plotWidth := width - left - right
	plotHeight := height - top - bottom
	duration := run.Times[len(run.Times)-1]
	colors := []string{"#e17845", "#2a8f83", "#c9a13b", "#5277a8"}

	view := chartView{
		Present:    true,
		Duration:   fmt.Sprintf("%.1f", run.Duration),
		SampleTime: fmt.Sprintf("%.3f", run.SampleTime),
		CreatedAt:  run.CreatedAt.Local().Format("15:04:05"),
		Metrics:    run.Metrics,
	}
	for index, series := range run.Series {
		var path strings.Builder
		for sample, value := range series.Values {
			x := left + (run.Times[sample]/duration)*plotWidth
			y := top + (maxY-value)/(maxY-minY)*plotHeight
			if sample == 0 {
				fmt.Fprintf(&path, "M %.2f %.2f", x, y)
			} else {
				fmt.Fprintf(&path, " L %.2f %.2f", x, y)
			}
		}
		view.Paths = append(view.Paths, chartPath{
			Name: series.Name, D: path.String(), Color: colors[index%len(colors)],
		})
	}
	for i := range 5 {
		fraction := float64(i) / 4
		value := maxY - fraction*(maxY-minY)
		view.YGrid = append(view.YGrid, chartGrid{
			Position: top + fraction*plotHeight,
			Label:    fmt.Sprintf("%.2g", value),
		})
	}
	for i := range 5 {
		fraction := float64(i) / 4
		view.XGrid = append(view.XGrid, chartGrid{
			Position: left + fraction*plotWidth,
			Label:    fmt.Sprintf("%.1f", fraction*duration),
		})
	}
	return view
}

func relativeTime(value time.Time) string {
	if value.IsZero() {
		return "just now"
	}
	delta := time.Since(value)
	if delta < time.Minute {
		return "just now"
	}
	if delta < time.Hour {
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	}
	return value.Local().Format("Jan 2, 15:04")
}
