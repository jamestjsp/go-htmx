package studio

import (
	"strconv"
	"strings"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

const maxDirectSignalWidth = 16

func normalizedDirectSignalWidth(parameters Parameters) int {
	if parameters.SignalWidth == 0 {
		return 1
	}
	return parameters.SignalWidth
}

func signalWidthField() parameterDefinition {
	return parameterDefinition{
		Name: "signal_width", Label: "Signal width", Type: "number",
		Step: "1", Min: "1", Max: strconv.Itoa(maxDirectSignalWidth),
		Unit: "channels", optional: true,
		set: func(parameters *Parameters, raw string) error {
			value, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil {
				return invalid("signal width must be a whole number")
			}
			if value < 1 || value > maxDirectSignalWidth {
				return invalid(
					"signal width must be between 1 and %d",
					maxDirectSignalWidth,
				)
			}
			parameters.SignalWidth = value
			return nil
		},
		text: func(parameters Parameters) string {
			return strconv.Itoa(normalizedDirectSignalWidth(parameters))
		},
	}
}

func validateDirectSignalWidth(parameters Parameters) error {
	width := normalizedDirectSignalWidth(parameters)
	if width < 1 || width > maxDirectSignalWidth {
		return invalid(
			"signal width must be between 1 and %d",
			maxDirectSignalWidth,
		)
	}
	return nil
}

func directSignalPort(width int) SignalPort {
	port, _ := newSignalPort(width, nil)
	return port
}

func directSignalPortSchema(inputPorts, inputWidth, outputWidth int) blockPortSchema {
	inputs := make([]SignalPort, inputPorts)
	for index := range inputs {
		inputs[index] = directSignalPort(inputWidth)
	}
	return blockPortSchema{
		inputs: inputs, outputs: []SignalPort{directSignalPort(outputWidth)},
	}
}

func parseSumSigns(raw string) (string, error) {
	value := strings.ReplaceAll(strings.TrimSpace(raw), " ", "")
	if count, err := strconv.Atoi(value); err == nil {
		if count < 1 || count > maxInputSigns {
			return "", invalid(
				"input count must be between 1 and %d",
				maxInputSigns,
			)
		}
		return strings.Repeat("+", count), nil
	}
	return value, nil
}

func directSumPortSchema(parameters Parameters) blockPortSchema {
	width := normalizedDirectSignalWidth(parameters)
	outputWidth := width
	if len(parameters.Signs) == 1 {
		outputWidth = 1
	}
	return directSignalPortSchema(len(parameters.Signs), width, outputWidth)
}

func realizeDirectSum(block Block, ports []int) (*controlsys.System, error) {
	width := normalizedDirectSignalWidth(block.Parameters)
	if len(block.Parameters.Signs) == 1 && len(ports) == 1 {
		gain := 1.0
		if block.Parameters.Signs[0] == '-' {
			gain = -1
		}
		values := make([]float64, width)
		for channel := range values {
			values[channel] = gain
		}
		return controlsys.NewGain(mat.NewDense(1, width, values), 0)
	}

	values := make([]float64, width*width*len(ports))
	for inputIndex, port := range ports {
		signIndex := min(port, len(block.Parameters.Signs)-1)
		gain := 1.0
		if block.Parameters.Signs[signIndex] == '-' {
			gain = -1
		}
		for channel := range width {
			values[channel*(width*len(ports))+inputIndex*width+channel] = gain
		}
	}
	return controlsys.NewGain(
		mat.NewDense(width, width*len(ports), values),
		0,
	)
}

func unitDelayInitialConditionField() parameterDefinition {
	return parameterDefinition{
		Name: "initial_condition", Label: "Initial condition", Type: "text",
		Placeholder: "0 or 1, 0",
		Help:        "Enter one value to broadcast, or one value per signal channel.",
		set: func(parameters *Parameters, raw string) error {
			value, err := ParseVectorValue(raw)
			if err != nil {
				return err
			}
			values := value.Values()
			if len(values) == 1 {
				parameters.InitialCondition = values[0]
				parameters.InitialState = nil
				return nil
			}
			parameters.InitialCondition = 0
			parameters.InitialState = &value
			return nil
		},
		text: func(parameters Parameters) string {
			if parameters.InitialState != nil {
				return parameters.InitialState.Text()
			}
			return formatFloat(parameters.InitialCondition)
		},
	}
}

func unitDelayInitialState(parameters Parameters) []float64 {
	width := normalizedDirectSignalWidth(parameters)
	if parameters.InitialState == nil {
		state := make([]float64, width)
		for index := range state {
			state[index] = parameters.InitialCondition
		}
		return state
	}
	values := parameters.InitialState.Values()
	if len(values) == 1 && width > 1 {
		state := make([]float64, width)
		for index := range state {
			state[index] = values[0]
		}
		return state
	}
	return values
}

func validateUnitDelayInitialState(parameters Parameters) error {
	if err := validateDirectSignalWidth(parameters); err != nil {
		return err
	}
	if parameters.InitialState == nil {
		return nil
	}
	values := parameters.InitialState.Values()
	width := normalizedDirectSignalWidth(parameters)
	if len(values) != 1 && len(values) != width {
		return invalid(
			"vector initial condition has %d values for signal width %d",
			len(values), width,
		)
	}
	return nil
}

func realizeVectorUnitDelay(parameters Parameters) (*controlsys.System, error) {
	width := normalizedDirectSignalWidth(parameters)
	return controlsys.New(
		mat.NewDense(width, width, nil),
		identityDense(width),
		identityDense(width),
		mat.NewDense(width, width, nil),
		parameters.SampleTime,
	)
}
