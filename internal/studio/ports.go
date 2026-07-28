package studio

import "fmt"

type SignalPort struct {
	Width    int
	Channels []string
}

func newSignalPort(width int, channels []string) (SignalPort, error) {
	if width <= 0 {
		return SignalPort{}, invalid("port width must be positive")
	}
	if len(channels) == 0 {
		channels = make([]string, width)
		for channel := range width {
			channels[channel] = defaultChannelName(channel, width)
		}
	}
	if len(channels) != width {
		return SignalPort{}, invalid(
			"port has width %d but %d channel names",
			width, len(channels),
		)
	}
	validated, err := NewChannelNames(channels)
	if err != nil {
		return SignalPort{}, err
	}
	return SignalPort{Width: width, Channels: validated.Names()}, nil
}

func defaultChannelName(channel, width int) string {
	if width == 1 {
		return "value"
	}
	return fmt.Sprintf("channel %d", channel+1)
}

func (port SignalPort) copy() SignalPort {
	return SignalPort{
		Width: port.Width, Channels: append([]string(nil), port.Channels...),
	}
}

type blockPortSchema struct {
	inputs  []SignalPort
	outputs []SignalPort
}

func (schema blockPortSchema) copy() blockPortSchema {
	copied := blockPortSchema{
		inputs:  make([]SignalPort, len(schema.inputs)),
		outputs: make([]SignalPort, len(schema.outputs)),
	}
	for i, port := range schema.inputs {
		copied.inputs[i] = port.copy()
	}
	for i, port := range schema.outputs {
		copied.outputs[i] = port.copy()
	}
	return copied
}

func scalarPortSchema(inputs, outputs int) blockPortSchema {
	schema := blockPortSchema{
		inputs:  make([]SignalPort, inputs),
		outputs: make([]SignalPort, outputs),
	}
	for i := range schema.inputs {
		schema.inputs[i], _ = newSignalPort(1, nil)
	}
	for i := range schema.outputs {
		schema.outputs[i], _ = newSignalPort(1, nil)
	}
	return schema
}

func (b Block) portSchema() blockPortSchema {
	definition, ok := blockDefinitions[b.Kind]
	if !ok {
		return blockPortSchema{}
	}
	return definition.ports(b.Parameters).copy()
}

func (b Block) InputPort(port int) (SignalPort, bool) {
	schema := b.portSchema()
	if port < 0 || port >= len(schema.inputs) {
		return SignalPort{}, false
	}
	return schema.inputs[port], true
}

func (b Block) OutputPort(port int) (SignalPort, bool) {
	schema := b.portSchema()
	if port < 0 || port >= len(schema.outputs) {
		return SignalPort{}, false
	}
	return schema.outputs[port], true
}

func resolvedInputPort(block Block, port int) (SignalPort, bool) {
	if schema, ok := block.InputPort(port); ok {
		return schema, true
	}
	definition := blockDefinitions[block.Kind]
	if definition.declareWiredPorts == nil || port < 0 {
		return SignalPort{}, false
	}
	widened, ok := definition.declareWiredPorts(block.Parameters, port+1)
	if !ok {
		return SignalPort{}, false
	}
	block.Parameters = widened
	return block.InputPort(port)
}

func validateConnectionWidth(source Block, sourcePort int, target Block, targetPort int) error {
	output, outputOK := source.OutputPort(sourcePort)
	input, inputOK := resolvedInputPort(target, targetPort)
	if !outputOK {
		return invalid("%s has no output port %d", source.Name, sourcePort)
	}
	if !inputOK {
		return invalid("%s has no input port %d", target.Name, targetPort)
	}
	if output.Width != input.Width {
		return invalid(
			"cannot connect %s output port %d (%d channels) to %s input port %d (%d channels)",
			source.Name, sourcePort, output.Width,
			target.Name, targetPort, input.Width,
		)
	}
	return nil
}
