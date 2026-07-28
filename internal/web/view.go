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

// registerView is the projects home — the drawing register. Every project the
// database holds arrives with the flowsheets its row expands to reveal, so
// expanding a row costs no request.
type registerView struct {
	Projects     []registerRowView
	ProjectCount int
	ProjectLabel string
	SheetCount   int
	SheetLabel   string
}

// registerRowView is one ruled line of the register.
type registerRowView struct {
	ID   int64
	Name string
	// Href is the project's own address. `GET /projects/{id}` already redirects
	// to the project's first flowsheet, so the name opens the project without
	// the register having to name a particular sheet — and it stays correct
	// when the first sheet changes.
	Href       string
	SheetCount int
	Edited     string
	Sheets     []registerSheetView
	// CanDelete carries the domain's refusal to delete the last project into
	// the interface, so the control is absent rather than present and doomed.
	CanDelete bool
	// Confirm names the project and its sheet count, which is what makes the
	// confirmation worth reading.
	Confirm string
}

// registerSheetView is one flowsheet chip under an expanded row.
type registerSheetView struct {
	// Ordinal is the sheet's place in the project's tab order, zero padded, the
	// way a drawing register numbers the sheets in a set. It is the tab strip's
	// own order, so the register and the workbench count sheets alike.
	Ordinal  string
	Name     string
	Href     string
	NeedsRun bool
}

func newRegisterView(register studio.Register) registerView {
	view := registerView{Projects: make([]registerRowView, 0, len(register.Projects))}
	// One project left is the one the domain refuses to delete.
	deletable := len(register.Projects) > 1
	for _, entry := range register.Projects {
		row := registerRowView{
			ID:         entry.Project.ID,
			Name:       entry.Project.Name,
			Href:       fmt.Sprintf("/projects/%d", entry.Project.ID),
			SheetCount: entry.FlowCount(),
			Edited:     relativeTime(entry.EditedAt),
			CanDelete:  deletable,
			Sheets:     make([]registerSheetView, 0, entry.FlowCount()),
		}
		row.Confirm = fmt.Sprintf("Delete “%s” and its %d %s? This cannot be undone.",
			row.Name, row.SheetCount, plural(row.SheetCount, "flowsheet", "flowsheets"),
		)
		for index, flow := range entry.Flows {
			row.Sheets = append(row.Sheets, registerSheetView{
				Ordinal:  fmt.Sprintf("%02d", index+1),
				Name:     flow.Name,
				Href:     fmt.Sprintf("/projects/%d/flows/%d", flow.ProjectID, flow.ID),
				NeedsRun: flow.NeedsRun,
			})
		}
		view.SheetCount += row.SheetCount
		view.Projects = append(view.Projects, row)
	}
	view.ProjectCount = len(view.Projects)
	view.ProjectLabel = plural(view.ProjectCount, "project", "projects")
	view.SheetLabel = plural(view.SheetCount, "sheet", "sheets")
	return view
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
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
	InputPorts    []portView
	OutputPorts   []portView
	Selected      bool
	ParameterText string
}

type portView struct {
	Index     int
	Top       int
	Center    int
	HitHeight int
	Size      int
	Label     string
	Name      string
}

type connectionView struct {
	studio.Connection
	Path         string
	SourceName   string
	TargetName   string
	SourceCenter int
	TargetCenter int
}

type inspectorLink struct {
	ID        int64
	Direction string
	OtherName string
	PortName  string
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
			InputPorts:    inputPortViews(block),
			OutputPorts:   outputPortViews(block),
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
			Connection:   connection,
			Path:         edgePath(source, connection.SourcePort, target, connection.TargetPort),
			SourceName:   blockNames[connection.SourceID],
			TargetName:   blockNames[connection.TargetID],
			SourceCenter: portCenterOffset(source.OutputPortCount(), connection.SourcePort),
			TargetCenter: portCenterOffset(target.InputPortCount(), connection.TargetPort),
		})
		portName := connectionPortName(source, connection.SourcePort, target, connection.TargetPort)
		if connection.SourceID == selectedID {
			view.SelectedLinks = append(view.SelectedLinks, inspectorLink{
				ID: connection.ID, Direction: "to", OtherName: blockNames[connection.TargetID],
				PortName: portName,
			})
		}
		if connection.TargetID == selectedID {
			view.SelectedLinks = append(view.SelectedLinks, inspectorLink{
				ID: connection.ID, Direction: "from", OtherName: blockNames[connection.SourceID],
				PortName: portName,
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

func inputPortViews(block studio.Block) []portView {
	ports := make([]portView, block.InputPortCount())
	for index := range ports {
		center := portCenterOffset(len(ports), index)
		size := portSize(len(ports))
		label := ""
		if block.Kind == studio.BlockSum && index < len(block.Parameters.Signs) {
			label = string(block.Parameters.Signs[index])
		}
		ports[index] = portView{
			Index: index, Top: portTop(center, size), Center: center,
			HitHeight: portHitHeight(len(ports)), Size: size,
			Label: label, Name: inputPortName(block, index),
		}
	}
	return ports
}

func outputPortViews(block studio.Block) []portView {
	ports := make([]portView, block.OutputPortCount())
	for index := range ports {
		center := portCenterOffset(len(ports), index)
		size := portSize(len(ports))
		ports[index] = portView{
			Index: index, Top: portTop(center, size), Center: center,
			HitHeight: portHitHeight(len(ports)), Size: size,
			Name: outputPortName(block, index),
		}
	}
	return ports
}

func inputPortName(block studio.Block, port int) string {
	if block.Kind == studio.BlockSum && port >= 0 && port < len(block.Parameters.Signs) {
		return fmt.Sprintf("input %s (port %d)", string(block.Parameters.Signs[port]), port+1)
	}
	return fmt.Sprintf("input port %d", port+1)
}

func outputPortName(_ studio.Block, port int) string {
	return fmt.Sprintf("output port %d", port+1)
}

func connectionPortName(source studio.Block, sourcePort int, target studio.Block, targetPort int) string {
	return fmt.Sprintf("%s ← %s",
		inputPortName(target, targetPort),
		outputPortName(source, sourcePort),
	)
}

func portCenterOffset(count, index int) int {
	if count <= 0 || index < 0 || index >= count {
		return studio.BlockHeight / 2
	}
	return int(math.Round(float64(studio.BlockHeight) * float64(index+1) / float64(count+1)))
}

func portHitHeight(count int) int {
	if count <= 1 {
		return studio.BlockHeight
	}
	return max(1, studio.BlockHeight/(count+1))
}

func portSize(count int) int {
	return min(14, portHitHeight(count))
}

func portTop(center, size int) int {
	if size == 14 {
		return center - 8
	}
	return center - size/2
}

func edgePath(source studio.Block, sourcePort int, target studio.Block, targetPort int) string {
	startX := float64(source.Position.X + studio.BlockWidth)
	startY := float64(source.Position.Y + portCenterOffset(source.OutputPortCount(), sourcePort))
	endX := float64(target.Position.X)
	endY := float64(target.Position.Y + portCenterOffset(target.InputPortCount(), targetPort))
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
