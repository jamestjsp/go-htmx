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
	Workspace       studio.Workspace
	Snapshot        studio.Snapshot
	Blocks          []blockView
	Connections     []connectionView
	Selected        *blockView
	SelectedLinks   []inspectorLink
	Palette         []paletteItem
	Sheet           sheetGeometry
	Tabs            []flowTabView
	Chart           chartView
	Error           string
	Updated         string
	BlockCount      int
	ConnectionCount int
}

// flowTabView is one sheet in the tab strip, in the project's `position`
// order.
//
// Both addresses are built here rather than in the template because a tab
// needs both and they are not interchangeable: Fragment is the workbench
// markup htmx swaps into the page, while Href is the canonical address the
// tab pushes, links to, and hands to a user without JavaScript. Pushing
// Fragment instead would put a bare <main> with no stylesheet in the address
// bar, which is what a reader gets back if they reload or share it.
type flowTabView struct {
	ID       int64
	Name     string
	Href     string
	Fragment string
	Active   bool
	// NeedsRun is the amber dot: the model changed after its last simulation.
	// It is the same flag the simulation dock reads, so the two cannot
	// disagree about whether the sheet is current.
	NeedsRun bool
}

// sheetGeometry hands the domain's sheet constants to the client so the
// viewport, the grid, and the snap step cannot drift from the server.
type sheetGeometry struct {
	Width       int
	Height      int
	Grid        int
	BlockWidth  int
	BlockHeight int
}

type blockView struct {
	studio.Block
	Definition    studio.BlockDefinition
	Fields        []studio.ParameterField
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
	studio.BlockDefinition
	X int
	Y int
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
	Spectra    []spectrumView
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

type spectrumView struct {
	Name          string
	D             string
	PeakFrequency string
	PeakMagnitude string
	MaxFrequency  string
}

func newWorkbenchView(workspace studio.Workspace, selectedID int64, errorMessage string) workbenchView {
	snapshot := workspace.Snapshot
	view := workbenchView{
		Workspace:       workspace,
		Snapshot:        snapshot,
		Error:           errorMessage,
		Updated:         relativeTime(snapshot.Flow.UpdatedAt),
		BlockCount:      len(snapshot.Blocks),
		ConnectionCount: len(snapshot.Connections),
		Sheet: sheetGeometry{
			Width:       studio.SheetWidth,
			Height:      studio.SheetHeight,
			Grid:        studio.GridPitch,
			BlockWidth:  studio.BlockWidth,
			BlockHeight: studio.BlockHeight,
		},
	}
	for _, flow := range workspace.Flows {
		view.Tabs = append(view.Tabs, flowTabView{
			ID:       flow.ID,
			Name:     flow.Name,
			Href:     fmt.Sprintf("/projects/%d/flows/%d", flow.ProjectID, flow.ID),
			Fragment: fmt.Sprintf("/flows/%d/workbench", flow.ID),
			Active:   flow.ID == snapshot.Flow.ID,
			NeedsRun: flow.NeedsRun,
		})
	}

	for _, definition := range studio.BlockLibrary() {
		view.Palette = append(view.Palette, paletteItem{
			BlockDefinition: definition,
			X:               60,
			Y:               80,
		})
	}

	blockNames := make(map[int64]string, len(snapshot.Blocks))
	for _, block := range snapshot.Blocks {
		blockNames[block.ID] = block.Name
		item := blockView{
			Block:         block,
			Definition:    block.Kind.Definition(),
			Fields:        block.EditorFields(),
			Selected:      block.ID == selectedID,
			ParameterText: block.Summary(),
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
	startX := float64(source.X + studio.BlockWidth)
	startY := float64(source.Y + studio.BlockHeight/2)
	endX := float64(target.X)
	endY := float64(target.Y + studio.BlockHeight/2)
	distance := math.Abs(endX - startX)
	bend := math.Max(54, distance*0.45)
	return fmt.Sprintf("M %.1f %.1f C %.1f %.1f, %.1f %.1f, %.1f %.1f",
		startX, startY, startX+bend, startY, endX-bend, endY, endX, endY)
}

func newChartView(run *studio.Simulation) chartView {
	if run == nil || len(run.Times) == 0 || len(run.Series) == 0 && len(run.Spectra) == 0 {
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
	colors := []string{"#e17845", "#2a8f83", "#c9a13b", "#5277a8"}

	view := chartView{
		Present:    true,
		Duration:   fmt.Sprintf("%.1f", run.Duration),
		SampleTime: fmt.Sprintf("%.3f", run.SampleTime),
		CreatedAt:  run.CreatedAt.Local().Format("15:04:05"),
		Metrics:    run.Metrics,
	}
	if len(run.Series) > 0 {
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
	}
	for _, spectrum := range run.Spectra {
		view.Spectra = append(view.Spectra, newSpectrumView(spectrum))
	}
	return view
}

func newSpectrumView(spectrum studio.Spectrum) spectrumView {
	const (
		left   = 48.0
		right  = 18.0
		top    = 18.0
		bottom = 30.0
		width  = 780.0
		height = 190.0
	)
	view := spectrumView{
		Name:          spectrum.Name,
		PeakFrequency: fmt.Sprintf("%.3g Hz", spectrum.PeakFrequency),
		PeakMagnitude: fmt.Sprintf("%.3g", spectrum.PeakMagnitude),
	}
	if len(spectrum.Frequencies) == 0 || len(spectrum.Magnitudes) == 0 {
		return view
	}
	maxFrequency := spectrum.Frequencies[len(spectrum.Frequencies)-1]
	maxMagnitude := spectrum.PeakMagnitude
	if maxFrequency <= 0 || maxMagnitude <= 0 {
		return view
	}
	view.MaxFrequency = fmt.Sprintf("%.3g Hz", maxFrequency)
	plotWidth := width - left - right
	plotHeight := height - top - bottom
	var path strings.Builder
	for i, frequency := range spectrum.Frequencies {
		x := left + frequency/maxFrequency*plotWidth
		y := top + (1-spectrum.Magnitudes[i]/maxMagnitude)*plotHeight
		if i == 0 {
			fmt.Fprintf(&path, "M %.2f %.2f", x, y)
		} else {
			fmt.Fprintf(&path, " L %.2f %.2f", x, y)
		}
	}
	view.D = path.String()
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
