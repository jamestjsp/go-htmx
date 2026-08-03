package studio

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
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

func TestParameterActivationDataDrivesValidationAndSchema(t *testing.T) {
	for _, kind := range blockOrder {
		definition := blockDefinitions[kind]
		schema, ok := kind.Schema()
		if !ok {
			t.Fatalf("%s: Schema() reported an unknown registered kind", kind)
		}
		for _, field := range definition.Parameters {
			field := field
			if len(field.activation) == 0 {
				continue
			}
			t.Run(string(kind)+"/"+field.Name, func(t *testing.T) {
				var published *ParameterSchema
				for index := range schema.Parameters {
					if schema.Parameters[index].Name == field.Name {
						published = &schema.Parameters[index]
						break
					}
				}
				if published == nil {
					t.Fatalf("schema does not publish %q", field.Name)
				}
				if !reflect.DeepEqual(published.ActiveWhen, field.activation) {
					t.Fatalf("published activation = %#v, want %#v", published.ActiveWhen, field.activation)
				}
				if field.active == nil {
					t.Fatal("activation has no derived validation predicate")
				}

				parameters := defaultParameters(kind)
				for _, condition := range field.activation {
					dependency := findParameterDefinition(definition.Parameters, condition.Name)
					if dependency == nil {
						t.Fatalf("activation names unknown parameter %q", condition.Name)
					}
					if len(condition.Values) == 0 {
						t.Fatalf("activation for %q has no activating values", condition.Name)
					}
					if err := dependency.set(&parameters, condition.Values[0]); err != nil {
						t.Fatalf("set %q to %q: %v", condition.Name, condition.Values[0], err)
					}
				}
				if !field.active(parameters, definition.Parameters) {
					t.Fatal("derived predicate is false for activating values")
				}

				for _, condition := range field.activation {
					dependency := findParameterDefinition(definition.Parameters, condition.Name)
					for _, option := range dependency.Options {
						activeValue := false
						for _, activatingValue := range condition.Values {
							if option.Value == activatingValue {
								activeValue = true
								break
							}
						}
						candidate := cloneParameters(parameters)
						if err := dependency.set(&candidate, option.Value); err != nil {
							t.Fatalf("set %q to option %q: %v", condition.Name, option.Value, err)
						}
						if field.active(candidate, definition.Parameters) != activeValue {
							t.Fatalf("predicate for %q = %v at %q, want %v", condition.Name, field.active(candidate, definition.Parameters), option.Value, activeValue)
						}
					}
				}
			})
		}
	}
}

func TestParameterActivationRejectsUnknownDependency(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil || !strings.Contains(fmt.Sprint(recovered), "undeclared parameter") {
			t.Fatalf("unknown activation dependency panic = %v", recovered)
		}
	}()
	parameterActivationsMatch(defaultParameters(BlockPID), []ParameterActivation{
		parameterActivation("missing_parameter", "value"),
	}, blockDefinitions[BlockPID].Parameters)
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
				if field.active != nil && !field.active(parameters, definition.Parameters) {
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
