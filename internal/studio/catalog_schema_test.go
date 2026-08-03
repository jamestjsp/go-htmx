package studio

import (
	"math"
	"reflect"
	"strconv"
	"testing"
)

func TestBlockKindSchemaOwnsEditableFieldsAndPorts(t *testing.T) {
	for _, kind := range blockOrder {
		t.Run(string(kind), func(t *testing.T) {
			schema, ok := kind.Schema()
			if !ok {
				t.Fatal("Schema() reported an unknown registered kind")
			}
			definition := blockDefinitions[kind]
			if len(schema.Parameters) != len(definition.Parameters) {
				t.Fatalf("schema has %d parameters, want %d", len(schema.Parameters), len(definition.Parameters))
			}
			for index, field := range definition.Parameters {
				published := schema.Parameters[index]
				if published.Name != field.Name {
					t.Fatalf("parameter %d name = %q, want %q", index, published.Name, field.Name)
				}
				if published.Default == "" && !field.optional {
					t.Fatalf("%s has no published default", field.Name)
				}
				minimum, maximum := publishedBounds(field)
				if !sameOptionalFloat(published.Minimum, minimum) || !sameOptionalFloat(published.Maximum, maximum) {
					t.Fatalf("%s bounds = %v..%v, want %v..%v", field.Name, published.Minimum, published.Maximum, minimum, maximum)
				}
			}

			block := Block{Kind: kind, Parameters: defaultParameters(kind)}
			if len(schema.Inputs) != block.InputPortCount() || len(schema.Outputs) != block.OutputPortCount() {
				t.Fatalf("ports = %d/%d, want %d/%d", len(schema.Inputs), len(schema.Outputs), block.InputPortCount(), block.OutputPortCount())
			}
			for index, published := range schema.Inputs {
				port, ok := block.InputPort(index)
				if !ok || published.Width != port.Width || !reflect.DeepEqual(published.Channels, port.Channels) {
					t.Fatalf("input port %d = %#v, want %#v", index, published, port)
				}
			}
			for index, published := range schema.Outputs {
				port, ok := block.OutputPort(index)
				if !ok || published.Width != port.Width || !reflect.DeepEqual(published.Channels, port.Channels) {
					t.Fatalf("output port %d = %#v, want %#v", index, published, port)
				}
			}
		})
	}
}

func TestBlockKindSchemaDefaultsRoundTripThroughValidation(t *testing.T) {
	for _, kind := range blockOrder {
		t.Run(string(kind), func(t *testing.T) {
			schema, ok := kind.Schema()
			if !ok {
				t.Fatal("Schema() reported an unknown registered kind")
			}
			values := make(map[string]string, len(schema.Parameters))
			for _, field := range schema.Parameters {
				if field.Default != "" {
					values[field.Name] = field.Default
				}
			}
			block := Block{Kind: kind, Name: "Default", Parameters: defaultParameters(kind)}
			if _, err := validateBlockUpdate(block, BlockUpdate{Name: "Default", Parameters: values}); err != nil {
				t.Fatalf("published defaults rejected: %v", err)
			}
		})
	}
}

func TestBlockKindSchemaBoundsRejectOneStepOutside(t *testing.T) {
	for _, kind := range blockOrder {
		definition := blockDefinitions[kind]
		for _, field := range definition.Parameters {
			if field.bound == nil {
				continue
			}
			t.Run(string(kind)+"/"+field.Name, func(t *testing.T) {
				parameters := defaultParameters(kind)
				if field.active != nil && !field.active(parameters) {
					t.Skip("field is inactive at the kind default")
				}
				schema, ok := kind.Schema()
				if !ok {
					t.Fatal("Schema() reported an unknown registered kind")
				}
				values := make(map[string]string, len(schema.Parameters))
				for _, published := range schema.Parameters {
					if published.Default != "" {
						values[published.Name] = published.Default
					}
				}
				if field.bound.max != nil {
					values[field.Name] = strconv.FormatFloat(*field.bound.max+outsideStep(*field.bound.max), 'g', -1, 64)
				} else if field.bound.min != nil {
					values[field.Name] = strconv.FormatFloat(*field.bound.min-outsideStep(*field.bound.min), 'g', -1, 64)
				} else {
					t.Skip("field has no finite bound")
				}
				block := Block{Kind: kind, Name: "Outside bound", Parameters: parameters}
				if _, err := validateBlockUpdate(block, BlockUpdate{Name: block.Name, Parameters: values}); err == nil {
					t.Fatal("value outside the published bound was accepted")
				}
			})
		}
	}
}

func sameOptionalFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func outsideStep(value float64) float64 {
	return math.Max(1, math.Abs(value)*0.1)
}
