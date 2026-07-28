package studio

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestVectorValueValidatesAndDefensivelyCopies(t *testing.T) {
	source := []float64{1, -2.5, math.SmallestNonzeroFloat64}
	value, err := NewVectorValue(source)
	if err != nil {
		t.Fatal(err)
	}
	source[0] = 99
	got := value.Values()
	got[1] = 88
	if want := []float64{1, -2.5, math.SmallestNonzeroFloat64}; !reflect.DeepEqual(value.Values(), want) {
		t.Fatalf("vector = %v, want %v", value.Values(), want)
	}
	if value.Text() != "1, -2.5, 5e-324" {
		t.Fatalf("vector text = %q", value.Text())
	}

	for _, bad := range [][]float64{nil, {math.NaN()}, {math.Inf(1)}} {
		if _, err := NewVectorValue(bad); err == nil {
			t.Fatalf("invalid vector %v succeeded", bad)
		}
	}
}

func TestNumericTextPreservesExactFloatValues(t *testing.T) {
	values := []float64{
		math.Nextafter(1, 2),
		math.Nextafter(-1, -2),
		math.SmallestNonzeroFloat64,
		math.MaxFloat64,
	}
	vector, err := NewVectorValue(values)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseVectorValue(vector.Text())
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range values {
		if got := decoded.Values()[i]; math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("value %d bits = %x, want %x", i, math.Float64bits(got), math.Float64bits(want))
		}
	}
}

func TestMatrixValueParsesShapeAndRoundTripsExactly(t *testing.T) {
	value, err := ParseMatrixValue("1, 2.5\n-3, 4")
	if err != nil {
		t.Fatal(err)
	}
	if rows, columns := value.Dims(); rows != 2 || columns != 2 {
		t.Fatalf("dimensions = %dx%d, want 2x2", rows, columns)
	}
	want := []float64{1, 2.5, -3, 4}
	if got := value.Values(); len(got) != len(want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
}

func TestMatrixValueUsesNewlinesOrSemicolonsAsRows(t *testing.T) {
	for _, raw := range []string{"1, 2\n3, 4", "1, 2; 3, 4"} {
		value, err := ParseMatrixValue(raw)
		if err != nil {
			t.Fatal(err)
		}
		if rows, columns := value.Dims(); rows != 2 || columns != 2 {
			t.Fatalf("%q dimensions = %dx%d, want 2x2", raw, rows, columns)
		}
		if value.Text() != "1, 2\n3, 4" {
			t.Fatalf("%q text = %q", raw, value.Text())
		}

		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded MatrixValue
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(decoded.Values(), value.Values()) {
			t.Fatalf("decoded values = %v, want %v", decoded.Values(), value.Values())
		}
	}
}

func TestMatrixValueRejectsMalformedAndMismatchedShapes(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"", "at least one row"},
		{"1, 2\n3", "row 2 has 1 columns; expected 2"},
		{"1, nope", `value "nope" is not a number`},
		{"1, NaN", "values must be finite"},
	}
	for _, test := range tests {
		_, err := ParseMatrixValue(test.raw)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("ParseMatrixValue(%q) error = %v, want %q", test.raw, err, test.want)
		}
	}
	if _, err := NewMatrixValue(2, 2, []float64{1, 2, 3}); err == nil {
		t.Fatal("shape mismatch succeeded")
	}
	if err := json.Unmarshal(
		[]byte(`{"rows":2,"columns":2,"values":[1,2,3]}`),
		new(MatrixValue),
	); err == nil {
		t.Fatal("invalid persisted matrix succeeded")
	}
}

func TestChannelNamesNormalizeAndRejectDuplicates(t *testing.T) {
	value, err := ParseChannelNames(" feed, product ; recycle ")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"feed", "product", "recycle"}; !reflect.DeepEqual(value.Names(), want) {
		t.Fatalf("names = %v, want %v", value.Names(), want)
	}
	copy := value.Names()
	copy[0] = "changed"
	if value.Names()[0] != "feed" {
		t.Fatal("Names exposed mutable storage")
	}
	for _, raw := range []string{"", "feed, feed", "feed,\n ,product"} {
		if _, err := ParseChannelNames(raw); err == nil {
			t.Fatalf("invalid names %q succeeded", raw)
		}
	}
}

func TestCatalogValueFieldsOwnParsingShapeAndText(t *testing.T) {
	matrixDefinition := matrixField("a", "A matrix", func(parameters *Parameters) **MatrixValue {
		return &parameters.A
	})
	namesDefinition := channelNamesField("inputs", "Input names", func(parameters *Parameters) **ChannelNames {
		return &parameters.InputNames
	})
	var parameters Parameters
	if err := matrixDefinition.set(&parameters, "1, 2\n3, 4"); err != nil {
		t.Fatal(err)
	}
	if err := namesDefinition.set(&parameters, "feed, recycle"); err != nil {
		t.Fatal(err)
	}
	if rows, columns := matrixDefinition.shape(parameters); rows != 2 || columns != 2 {
		t.Fatalf("matrix field shape = %dx%d", rows, columns)
	}
	if matrixDefinition.text(parameters) != "1, 2\n3, 4" {
		t.Fatalf("matrix field text = %q", matrixDefinition.text(parameters))
	}
	if rows, columns := namesDefinition.shape(parameters); rows != 1 || columns != 2 {
		t.Fatalf("names field shape = %dx%d", rows, columns)
	}

	cloned := cloneParameters(parameters)
	cloned.A.values[0] = 99
	cloned.InputNames.names[0] = "changed"
	if parameters.A.At(0, 0) != 1 || parameters.InputNames.Names()[0] != "feed" {
		t.Fatal("cloneParameters shared validated value storage")
	}
}

func TestParametersJSONKeepsLegacyCoefficientsAndValidatedValues(t *testing.T) {
	a, err := ParseMatrixValue("1, 0\n0, 1")
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ParseChannelNames("u1, u2")
	if err != nil {
		t.Fatal(err)
	}
	parameters := Parameters{
		Numerator:   []float64{1, 2},
		Denominator: []float64{1, 3, 2},
		A:           &a,
		InputNames:  &inputs,
	}
	encoded, err := encodeParameters(parameters)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeParameters(BlockTransfer, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Numerator, parameters.Numerator) ||
		!reflect.DeepEqual(decoded.Denominator, parameters.Denominator) ||
		!reflect.DeepEqual(decoded.A.Values(), parameters.A.Values()) ||
		!reflect.DeepEqual(decoded.InputNames.Names(), parameters.InputNames.Names()) {
		t.Fatalf("decoded parameters = %#v", decoded)
	}

	legacy, err := decodeParameters(
		BlockTransfer,
		`{"numerator":[2,1],"denominator":[1,3,2]}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(legacy.Numerator, []float64{2, 1}) ||
		!reflect.DeepEqual(legacy.Denominator, []float64{1, 3, 2}) {
		t.Fatalf("legacy coefficients = %v / %v", legacy.Numerator, legacy.Denominator)
	}
}

func TestTransferCoefficientEditorReportsRowShape(t *testing.T) {
	block := Block{Kind: BlockTransfer, Parameters: Parameters{
		Numerator: []float64{1, 2}, Denominator: []float64{1, 3, 2},
	}}
	fields := block.EditorFields()
	if fields[0].Rows != 1 || fields[0].Columns != 2 ||
		fields[1].Rows != 1 || fields[1].Columns != 3 {
		t.Fatalf("coefficient shapes = %#v", fields)
	}
}
