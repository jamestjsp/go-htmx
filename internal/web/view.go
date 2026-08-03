package web

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jamestjsp/process-lab/internal/studio"
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
	Workspace             studio.Workspace
	Snapshot              studio.Snapshot
	Blocks                []blockView
	Connections           []connectionView
	Selected              *blockView
	SelectedLinks         []inspectorLink
	Palette               []paletteItem
	Sheet                 sheetGeometry
	Tabs                  []flowTabView
	Chart                 chartView
	Analysis              analysisView
	ControllerCandidate   *controllerCandidateView
	Error                 string
	Updated               string
	BlockCount            int
	ConnectionCount       int
	SimulationLimits      string
	SimulationMinDuration float64
	SimulationMinSample   float64
	BoundedEdit           bool

	// Title names the flowsheet the page is showing. A tab swap pushes a new
	// URL, so the title has to move with it or every history entry reads the
	// same and the change is announced to nobody. The workbench fragment
	// carries it as a root-level <title>, which htmx lifts out of a partial
	// and applies to the document.
	Title string
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
	Width     int
	Channels  []string
}

type connectionView struct {
	studio.Connection
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
	Fidelity   fidelityView
}

type fidelityView struct {
	Driver     string
	Domain     string
	BaseStep   string
	SourceHold string
	Segments   int
	Rates      []string
	Delays     []string
	Note       string
}

type chartPath struct {
	Name  string
	Key   string
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

type analysisView struct {
	Available bool
	Inputs    []analysisChannelOptionView
	Outputs   []analysisChannelOptionView
	Results   []analysisResultView
	Stale     bool
	Revision  string
}

type analysisChannelOptionView struct {
	Value    string
	Name     string
	Selected bool
}

type analysisResultView struct {
	Kind     string
	Title    string
	Created  string
	Revision string
	Channel  string
	Stale    bool
	Metrics  []analysisMetricView
	Plots    []analysisPlotView
	Notices  []string
}

type analysisMetricView struct {
	Label string
	Value string
}

type analysisPlotView struct {
	Title   string
	XLabel  string
	YLabel  string
	Paths   []chartPath
	Markers []analysisMarkerView
}

type analysisMarkerView struct {
	X     float64
	Y     float64
	Label string
	Kind  string
}

// workbenchTitle names the sheet before the project so a browser tab, a
// history entry, and a bookmark stay distinguishable when the label is
// truncated to its first few characters.
func workbenchTitle(workspace studio.Workspace) string {
	flow := strings.TrimSpace(workspace.Snapshot.Flow.Name)
	project := strings.TrimSpace(workspace.Project.Name)
	switch {
	case flow == "" && project == "":
		return "Process Lab"
	case flow == "":
		return project + " · Process Lab"
	case project == "":
		return flow + " · Process Lab"
	}
	return flow + " · " + project + " · Process Lab"
}

func newWorkbenchView(workspace studio.Workspace, selectedID int64, errorMessage string) workbenchView {
	snapshot := workspace.Snapshot
	view := workbenchView{
		Workspace:             workspace,
		Snapshot:              snapshot,
		Title:                 workbenchTitle(workspace),
		Error:                 errorMessage,
		Updated:               relativeTime(snapshot.Flow.UpdatedAt),
		BlockCount:            len(snapshot.Blocks),
		ConnectionCount:       len(snapshot.Connections),
		SimulationLimits:      studio.SimulationLimitsText(),
		SimulationMinDuration: studio.MinSimulationDuration,
		SimulationMinSample:   studio.MinSimulationSampleTime,
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
	view.Analysis = newAnalysisView(workspace.Analysis)
	return view
}

func newAnalysisView(workspace studio.AnalysisWorkspace) analysisView {
	view := analysisView{
		Available: len(workspace.Inputs) > 0 && len(workspace.Outputs) > 0,
		Revision:  workspace.ModelUpdatedAt.Local().Format("15:04:05.000"),
	}
	for _, channel := range workspace.Inputs {
		view.Inputs = append(view.Inputs, analysisChannelOptionView{
			Value:    channelRefValue(channel.ChannelRef),
			Name:     channel.Name,
			Selected: channel.ChannelRef == workspace.SelectedInput,
		})
	}
	for _, channel := range workspace.Outputs {
		view.Outputs = append(view.Outputs, analysisChannelOptionView{
			Value:    channelRefValue(channel.ChannelRef),
			Name:     channel.Name,
			Selected: channel.ChannelRef == workspace.SelectedOutput,
		})
	}
	if workspace.Dynamics != nil {
		view.Results = append(view.Results, dynamicsResultView(*workspace.Dynamics))
		view.Stale = view.Stale || workspace.Dynamics.Stale
	}
	if workspace.Frequency != nil {
		view.Results = append(view.Results, frequencyResultView(*workspace.Frequency))
		view.Stale = view.Stale || workspace.Frequency.Stale
	}
	if workspace.Loop != nil {
		view.Results = append(view.Results, loopResultView(*workspace.Loop))
		view.Stale = view.Stale || workspace.Loop.Stale
	}
	return view
}

func dynamicsResultView(record studio.DynamicsAnalysisRecord) analysisResultView {
	result := record.Result
	view := analysisResultView{
		Kind:     "dynamics",
		Title:    "Dynamics & time",
		Created:  record.CreatedAt.Local().Format("15:04:05"),
		Revision: result.ModelUpdatedAt.Local().Format("15:04:05.000"),
		Channel:  result.Input.Name + " → " + result.Output.Name,
		Stale:    record.Stale,
	}
	if result.Stable != nil {
		value := "unstable"
		if *result.Stable {
			value = "stable"
		}
		view.Metrics = append(view.Metrics, analysisMetricView{Label: "Stability", Value: value})
	}
	if result.DCGain != nil {
		view.Metrics = append(view.Metrics, analysisMetricView{
			Label: "DC gain", Value: formatAnalysisNumber(*result.DCGain),
		})
	}
	view.Metrics = append(view.Metrics,
		analysisMetricView{Label: "Poles", Value: fmt.Sprintf("%d", len(result.Poles))},
		analysisMetricView{Label: "Zeros", Value: fmt.Sprintf("%d", len(result.Zeros))},
	)
	if result.StepExperiment != nil {
		step := result.StepExperiment
		view.Plots = append(view.Plots, analysisLinePlot(
			"Step response", "time (s)", "output",
			[]analysisSeries{{
				Name: "step", Color: "#e17845",
				X: step.Times, Y: step.Values,
			}},
			nil,
		))
		if step.Metrics.RiseTime != nil {
			view.Metrics = append(view.Metrics, analysisMetricView{
				Label: "Rise time", Value: formatAnalysisNumber(*step.Metrics.RiseTime) + " s",
			})
		}
		if step.Metrics.SettlingTime != nil {
			view.Metrics = append(view.Metrics, analysisMetricView{
				Label: "Settling", Value: formatAnalysisNumber(*step.Metrics.SettlingTime) + " s",
			})
		}
	}
	if len(result.Poles) > 0 || len(result.Zeros) > 0 {
		var markers []analysisPoint
		for _, pole := range result.Poles {
			markers = append(markers, analysisPoint{
				X: pole.Real, Y: pole.Imag, Label: "×", Kind: "pole",
			})
		}
		for _, zero := range result.Zeros {
			markers = append(markers, analysisPoint{
				X: zero.Real, Y: zero.Imag, Label: "○", Kind: "zero",
			})
		}
		view.Plots = append(view.Plots, analysisLinePlot(
			"Pole-zero map", "real", "imaginary", nil, markers,
		))
	}
	for _, issue := range result.Issues {
		view.Notices = append(view.Notices, issue.Operation+": "+issue.Message)
	}
	return view
}

func frequencyResultView(record studio.FrequencyAnalysisRecord) analysisResultView {
	result := record.Result
	view := analysisResultView{
		Kind:     "frequency",
		Title:    "Frequency response",
		Created:  record.CreatedAt.Local().Format("15:04:05"),
		Revision: result.ModelUpdatedAt.Local().Format("15:04:05.000"),
		Stale:    record.Stale,
	}
	if len(result.Inputs) > 0 && len(result.Outputs) > 0 {
		if len(result.Inputs) == 1 && len(result.Outputs) == 1 {
			view.Channel = result.Inputs[0].Name + " → " + result.Outputs[0].Name
		} else {
			view.Channel = fmt.Sprintf(
				"%d named inputs → %d named outputs",
				len(result.Inputs), len(result.Outputs),
			)
		}
	}
	view.Metrics = append(view.Metrics,
		analysisMetricView{Label: "Grid", Value: fmt.Sprintf("%d points", len(result.Grid.Omega))},
		analysisMetricView{Label: "Frequency", Value: result.Units.Frequency},
		analysisMetricView{Label: "Magnitude", Value: result.Units.Magnitude},
	)
	if len(result.Bode) > 0 {
		magnitudeSeries := make([]analysisSeries, 0, len(result.Bode))
		phaseSeries := make([]analysisSeries, 0, len(result.Bode))
		for index, trace := range result.Bode {
			if trace.InputIndex < 0 || trace.InputIndex >= len(result.Inputs) ||
				trace.OutputIndex < 0 || trace.OutputIndex >= len(result.Outputs) {
				continue
			}
			input := result.Inputs[trace.InputIndex]
			output := result.Outputs[trace.OutputIndex]
			name := output.Name + " ← " + input.Name
			key := fmt.Sprintf(
				"frequency:%d:%d:%d:%d:%d:%d",
				output.BlockID, output.Port, output.Channel,
				input.BlockID, input.Port, input.Channel,
			)
			color := chartColors[index%len(chartColors)]
			x := transformedFrequencies(result.Grid.Omega)
			magnitudeSeries = append(magnitudeSeries, analysisSeries{
				Name: name, Key: key, Color: color, X: x,
				Y: pointerValues(trace.MagnitudeDB),
			})
			phaseSeries = append(phaseSeries, analysisSeries{
				Name: name, Key: key, Color: color, X: x,
				Y: pointerValues(trace.PhaseDegrees),
			})
		}
		view.Plots = append(view.Plots,
			analysisLinePlot(
				"Bode magnitude", "log₁₀ ω", "dB", magnitudeSeries, nil,
			),
			analysisLinePlot(
				"Bode phase", "log₁₀ ω", "degrees", phaseSeries, nil,
			),
		)
	}
	if result.Nyquist != nil {
		view.Plots = append(view.Plots, complexSamplePlot(
			"Nyquist", "real", "imaginary",
			result.Nyquist.Positive, "#2a8f83",
		))
	}
	if result.Nichols != nil {
		view.Plots = append(view.Plots, pointerXYPlot(
			"Nichols", "phase (deg)", "magnitude (dB)",
			result.Nichols.PhaseDegrees,
			result.Nichols.MagnitudeDB,
			"#c9a13b",
		))
	}
	if result.SingularValues != nil {
		var series []analysisSeries
		for index, values := range result.SingularValues.Values {
			series = append(series, analysisSeries{
				Name:  fmt.Sprintf("σ%d", index+1),
				Key:   fmt.Sprintf("frequency:sigma:%d", index+1),
				Color: chartColors[index%len(chartColors)],
				X:     transformedFrequencies(result.Grid.Omega),
				Y:     pointerValues(values),
			})
		}
		view.Plots = append(view.Plots, analysisLinePlot(
			"Singular values", "log₁₀ ω", "absolute gain", series, nil,
		))
	}
	for _, issue := range result.Issues {
		view.Notices = append(view.Notices, issue.Operation+": "+issue.Message)
	}
	return view
}

func loopResultView(record studio.LoopAnalysisRecord) analysisResultView {
	result := record.Result
	view := analysisResultView{
		Kind:     "loop",
		Title:    "Loop robustness",
		Created:  record.CreatedAt.Local().Format("15:04:05"),
		Revision: result.ModelUpdatedAt.Local().Format("15:04:05.000"),
		Channel:  result.Input.Name + " → " + result.Output.Name,
		Stale:    record.Stale,
		Metrics: []analysisMetricView{
			{Label: "Basis", Value: "explicit SISO"},
			{Label: "Domain", Value: result.Domain},
		},
	}
	if result.Margins != nil {
		view.Metrics = append(view.Metrics,
			analysisMetricView{
				Label: "Gain margin", Value: formatOptionalAnalysisNumber(result.Margins.GainMarginDB, "unbounded", " dB"),
			},
			analysisMetricView{
				Label: "Phase margin", Value: formatOptionalAnalysisNumber(result.Margins.PhaseMarginDegrees, "unbounded", "°"),
			},
		)
	}
	if result.Bandwidth != nil {
		value := formatOptionalAnalysisNumber(result.Bandwidth.RadPerSecond, "unbounded", " rad/s")
		view.Metrics = append(view.Metrics, analysisMetricView{Label: "Bandwidth", Value: value})
	}
	if result.DiskMargin != nil {
		view.Metrics = append(view.Metrics, analysisMetricView{
			Label: "Peak sensitivity",
			Value: formatOptionalAnalysisNumber(result.DiskMargin.PeakSensitivity, "undefined", ""),
		})
	}
	if result.Passivity != nil {
		view.Metrics = append(view.Metrics, analysisMetricView{
			Label: "Passivity evidence", Value: result.Passivity.Status,
		})
	}
	if result.RootLocus != nil {
		var series []analysisSeries
		for index, branch := range result.RootLocus.Branches {
			x := make([]float64, len(branch))
			y := make([]float64, len(branch))
			for sample, value := range branch {
				x[sample], y[sample] = value.Real, value.Imag
			}
			series = append(series, analysisSeries{
				Name: fmt.Sprintf("branch %d", index+1), Color: chartColors[index%len(chartColors)],
				X: x, Y: y,
			})
		}
		view.Plots = append(view.Plots, analysisLinePlot(
			"Root locus", "real", "imaginary", series, nil,
		))
	}
	for _, applicability := range result.Applicability {
		if applicability.Status != "available" {
			view.Notices = append(view.Notices,
				applicability.Operation+": "+applicability.Detail,
			)
		}
	}
	return view
}

const analysisPlotWidth = 400.0
const analysisPlotHeight = 140.0

var chartColors = []string{"#e17845", "#2a8f83", "#c9a13b", "#5277a8"}

type analysisSeries struct {
	Name  string
	Key   string
	Color string
	X     []float64
	Y     []float64
}

type analysisPoint struct {
	X     float64
	Y     float64
	Label string
	Kind  string
}

func analysisLinePlot(
	title string,
	xLabel string,
	yLabel string,
	series []analysisSeries,
	points []analysisPoint,
) analysisPlotView {
	plot := analysisPlotView{Title: title, XLabel: xLabel, YLabel: yLabel}
	minX, maxX, minY, maxY, ok := analysisBounds(series, points)
	if !ok {
		return plot
	}
	for _, values := range series {
		var path strings.Builder
		started := false
		for i := 0; i < len(values.X) && i < len(values.Y); i++ {
			x, y := values.X[i], values.Y[i]
			if !finiteViewNumber(x) || !finiteViewNumber(y) {
				started = false
				continue
			}
			command := "L"
			if !started {
				command = "M"
				started = true
			}
			fmt.Fprintf(&path, "%s %.2f %.2f ", command,
				scaleAnalysis(x, minX, maxX, 18, analysisPlotWidth-10),
				scaleAnalysis(y, minY, maxY, analysisPlotHeight-16, 10),
			)
		}
		if path.Len() > 0 {
			plot.Paths = append(plot.Paths, chartPath{
				Name: values.Name, Key: values.Key,
				D: strings.TrimSpace(path.String()), Color: values.Color,
			})
		}
	}
	for _, point := range points {
		plot.Markers = append(plot.Markers, analysisMarkerView{
			X:     scaleAnalysis(point.X, minX, maxX, 18, analysisPlotWidth-10),
			Y:     scaleAnalysis(point.Y, minY, maxY, analysisPlotHeight-16, 10),
			Label: point.Label,
			Kind:  point.Kind,
		})
	}
	return plot
}

func analysisBounds(
	series []analysisSeries,
	points []analysisPoint,
) (float64, float64, float64, float64, bool) {
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	add := func(x, y float64) {
		if !finiteViewNumber(x) || !finiteViewNumber(y) {
			return
		}
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	for _, values := range series {
		for i := 0; i < len(values.X) && i < len(values.Y); i++ {
			add(values.X[i], values.Y[i])
		}
	}
	for _, point := range points {
		add(point.X, point.Y)
	}
	if math.IsInf(minX, 1) {
		return 0, 0, 0, 0, false
	}
	minX, maxX = paddedAnalysisRange(minX, maxX)
	minY, maxY = paddedAnalysisRange(minY, maxY)
	return minX, maxX, minY, maxY, true
}

func paddedAnalysisRange(minimum, maximum float64) (float64, float64) {
	if minimum == maximum {
		padding := math.Max(math.Abs(minimum)*0.1, 1)
		return minimum - padding, maximum + padding
	}
	padding := (maximum - minimum) * 0.05
	return minimum - padding, maximum + padding
}

func scaleAnalysis(value, minimum, maximum, low, high float64) float64 {
	return low + (value-minimum)*(high-low)/(maximum-minimum)
}

func analysisPointerPlot(
	title, xLabel, yLabel string,
	x []float64,
	y []*float64,
	logX bool,
	color string,
) analysisPlotView {
	values := pointerValues(y)
	if logX {
		x = transformedFrequencies(x)
	}
	return analysisLinePlot(title, xLabel, yLabel, []analysisSeries{{
		Name: title, Color: color, X: x, Y: values,
	}}, nil)
}

func pointerXYPlot(
	title, xLabel, yLabel string,
	x, y []*float64,
	color string,
) analysisPlotView {
	return analysisLinePlot(title, xLabel, yLabel, []analysisSeries{{
		Name: title, Color: color, X: pointerValues(x), Y: pointerValues(y),
	}}, nil)
}

func complexSamplePlot(
	title, xLabel, yLabel string,
	values []studio.ComplexSample,
	color string,
) analysisPlotView {
	x := make([]float64, len(values))
	y := make([]float64, len(values))
	for i, value := range values {
		x[i], y[i] = math.NaN(), math.NaN()
		if value.Real != nil && value.Imag != nil {
			x[i], y[i] = *value.Real, *value.Imag
		}
	}
	return analysisLinePlot(title, xLabel, yLabel, []analysisSeries{{
		Name: title, Color: color, X: x, Y: y,
	}}, nil)
}

func pointerValues(values []*float64) []float64 {
	result := make([]float64, len(values))
	for i, value := range values {
		result[i] = math.NaN()
		if value != nil {
			result[i] = *value
		}
	}
	return result
}

func transformedFrequencies(values []float64) []float64 {
	result := make([]float64, len(values))
	for i, value := range values {
		result[i] = math.Log10(value)
	}
	return result
}

func finiteViewNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func channelRefValue(ref studio.ChannelRef) string {
	return fmt.Sprintf("%d:%d:%d", ref.BlockID, ref.Port, ref.Channel)
}

func formatAnalysisNumber(value float64) string {
	return fmt.Sprintf("%.4g", value)
}

func formatOptionalAnalysisNumber(value *float64, fallback, suffix string) string {
	if value == nil {
		return fallback
	}
	return formatAnalysisNumber(*value) + suffix
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
		if (block.Kind == studio.BlockSum || block.Kind == studio.BlockVectorSum) &&
			index < len(block.Parameters.Signs) {
			label = string(block.Parameters.Signs[index])
		}
		schema, _ := block.InputPort(index)
		if schema.Width > 1 {
			label = fmt.Sprintf("%d", schema.Width)
		}
		ports[index] = portView{
			Index: index, Top: portTop(center, size), Center: center,
			HitHeight: portHitHeight(len(ports)), Size: size,
			Label: label, Name: inputPortName(block, index),
			Width: schema.Width, Channels: schema.Channels,
		}
	}
	return ports
}

func outputPortViews(block studio.Block) []portView {
	ports := make([]portView, block.OutputPortCount())
	for index := range ports {
		center := portCenterOffset(len(ports), index)
		size := portSize(len(ports))
		schema, _ := block.OutputPort(index)
		label := ""
		if schema.Width > 1 {
			label = fmt.Sprintf("%d", schema.Width)
		}
		ports[index] = portView{
			Index: index, Top: portTop(center, size), Center: center,
			HitHeight: portHitHeight(len(ports)), Size: size,
			Label: label, Name: outputPortName(block, index),
			Width: schema.Width, Channels: schema.Channels,
		}
	}
	return ports
}

func inputPortName(block studio.Block, port int) string {
	if (block.Kind == studio.BlockSum || block.Kind == studio.BlockVectorSum) &&
		port >= 0 && port < len(block.Parameters.Signs) {
		return fmt.Sprintf("input %s (port %d)", string(block.Parameters.Signs[port]), port+1)
	}
	return portName("input", block, port)
}

func outputPortName(block studio.Block, port int) string {
	return portName("output", block, port)
}

func portName(direction string, block studio.Block, port int) string {
	var (
		schema studio.SignalPort
		ok     bool
	)
	if direction == "input" {
		schema, ok = block.InputPort(port)
	} else {
		schema, ok = block.OutputPort(port)
	}
	if !ok || schema.Width == 1 {
		return fmt.Sprintf("%s port %d", direction, port+1)
	}
	return fmt.Sprintf(
		"%s port %d (%d channels: %s)",
		direction, port+1, schema.Width, strings.Join(schema.Channels, ", "),
	)
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
		Fidelity:   newFidelityView(run.Fidelity, run.SampleTime),
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
				Name: series.Name,
				Key: fmt.Sprintf(
					"%d:%d:%d", series.BlockID, series.Port, series.Channel,
				),
				D: path.String(), Color: colors[index%len(colors)],
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

func newFidelityView(fidelity studio.Fidelity, fallbackBaseStep float64) fidelityView {
	if fidelity.BaseStep == 0 {
		fidelity.BaseStep = fallbackBaseStep
	}
	if fidelity.ModelDomain == "" {
		fidelity.ModelDomain = "continuous"
	}
	if fidelity.SegmentCount == 0 {
		fidelity.SegmentCount = 1
	}
	if fidelity.Driver == "" {
		fidelity.Driver = "batch-lsim"
	}
	if fidelity.SourceHold == "" {
		fidelity.SourceHold = "piecewise-constant"
	}
	view := fidelityView{
		Driver:     fidelity.Driver,
		Domain:     fidelity.ModelDomain,
		BaseStep:   fmt.Sprintf("%.3g s", fidelity.BaseStep),
		SourceHold: strings.ReplaceAll(fidelity.SourceHold, "-", " "),
		Segments:   fidelity.SegmentCount,
	}
	switch fidelity.Driver {
	case "batch-lsim":
		view.Driver = "Batch LTI · Lsim"
	case "delay-aware-simulate":
		view.Driver = "Delay-aware · Simulate"
	case "per-sample-simulate":
		view.Driver = "Stateful discrete · Simulate"
	}
	for _, rate := range fidelity.BlockRates {
		timing := fmt.Sprintf("%.3g s · %s", rate.SampleTime, rate.Mode)
		if rate.UpdateEvery > 1 {
			timing = fmt.Sprintf("%.3g s · every %d base steps", rate.SampleTime, rate.UpdateEvery)
		}
		view.Rates = append(view.Rates, fmt.Sprintf("%s · %s", rate.BlockName, timing))
	}
	for _, delay := range fidelity.Delays {
		switch delay.Representation {
		case "exact":
			view.Delays = append(view.Delays, fmt.Sprintf(
				"%s · exact %.3g s · aligned at %.3g s",
				delay.BlockName, delay.Delay, delay.SampleTime,
			))
		case "pade":
			view.Delays = append(view.Delays, fmt.Sprintf(
				"%s · Padé %d · %.3g s",
				delay.BlockName, delay.ApproximationOrder, delay.Delay,
			))
		case "thiran":
			view.Delays = append(view.Delays, fmt.Sprintf(
				"%s · Thiran %d · %.3g s at %.3g s",
				delay.BlockName, delay.ApproximationOrder,
				delay.Delay, delay.SampleTime,
			))
		}
	}
	switch {
	case fidelity.SourceHold == "sampled-zero-order-hold":
		view.Note = "Sampled source values are held between run points."
	case fidelity.SegmentCount > 1:
		view.Note = "Segment boundaries use a zero-order hold."
	case hasApproximateDelay(fidelity.Delays):
		view.Note = "Delay behavior includes an explicit finite-order approximation."
	case fidelity.Driver == "per-sample-simulate":
		view.Note = "controlsys carries discrete state between samples."
	default:
		view.Note = "One composed LTI segment with piecewise-constant excitation."
	}
	return view
}

func hasApproximateDelay(delays []studio.DelayProvenance) bool {
	for _, delay := range delays {
		if delay.Representation != "exact" {
			return true
		}
	}
	return false
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
