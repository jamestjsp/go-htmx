package studio

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type VectorValue struct {
	values []float64
}

func NewVectorValue(values []float64) (VectorValue, error) {
	if len(values) == 0 {
		return VectorValue{}, invalid("vector must contain at least one value")
	}
	copied := append([]float64(nil), values...)
	for _, value := range copied {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return VectorValue{}, invalid("vector values must be finite")
		}
	}
	return VectorValue{values: copied}, nil
}

func ParseVectorValue(raw string) (VectorValue, error) {
	values, err := parseNumericRow(raw)
	if err != nil {
		return VectorValue{}, err
	}
	return NewVectorValue(values)
}

func (value VectorValue) Len() int {
	return len(value.values)
}

func (value VectorValue) Values() []float64 {
	return append([]float64(nil), value.values...)
}

func (value VectorValue) Text() string {
	return numericRowText(value.values)
}

func (value VectorValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(value.values)
}

func (value *VectorValue) UnmarshalJSON(encoded []byte) error {
	var values []float64
	if err := json.Unmarshal(encoded, &values); err != nil {
		return err
	}
	validated, err := NewVectorValue(values)
	if err != nil {
		return err
	}
	*value = validated
	return nil
}

type MatrixValue struct {
	rows, columns int
	values        []float64
}

func NewMatrixValue(rows, columns int, values []float64) (MatrixValue, error) {
	if rows <= 0 || columns <= 0 {
		return MatrixValue{}, invalid("matrix rows and columns must be positive")
	}
	if len(values) != rows*columns {
		return MatrixValue{}, invalid(
			"matrix has %d values for %d rows by %d columns",
			len(values), rows, columns,
		)
	}
	copied := append([]float64(nil), values...)
	for _, value := range copied {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return MatrixValue{}, invalid("matrix values must be finite")
		}
	}
	return MatrixValue{rows: rows, columns: columns, values: copied}, nil
}

func ParseMatrixValue(raw string) (MatrixValue, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n")
	rowsText := strings.FieldsFunc(normalized, func(r rune) bool {
		return r == '\n' || r == ';'
	})
	if len(rowsText) == 0 {
		return MatrixValue{}, invalid("matrix must contain at least one row")
	}
	var (
		columns int
		values  []float64
	)
	for rowIndex, rowText := range rowsText {
		row, err := parseNumericRow(rowText)
		if err != nil {
			return MatrixValue{}, invalid("matrix row %d: %s", rowIndex+1, err)
		}
		if rowIndex == 0 {
			columns = len(row)
		} else if len(row) != columns {
			return MatrixValue{}, invalid(
				"matrix row %d has %d columns; expected %d",
				rowIndex+1, len(row), columns,
			)
		}
		values = append(values, row...)
	}
	return NewMatrixValue(len(rowsText), columns, values)
}

func (value MatrixValue) Dims() (rows, columns int) {
	return value.rows, value.columns
}

func (value MatrixValue) At(row, column int) float64 {
	if row < 0 || row >= value.rows || column < 0 || column >= value.columns {
		panic("studio: matrix index out of range")
	}
	return value.values[row*value.columns+column]
}

func (value MatrixValue) Values() []float64 {
	return append([]float64(nil), value.values...)
}

func (value MatrixValue) Text() string {
	rows := make([]string, value.rows)
	for row := range value.rows {
		start := row * value.columns
		rows[row] = numericRowText(value.values[start : start+value.columns])
	}
	return strings.Join(rows, "\n")
}

func (value MatrixValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Rows    int       `json:"rows"`
		Columns int       `json:"columns"`
		Values  []float64 `json:"values"`
	}{
		Rows: value.rows, Columns: value.columns, Values: value.values,
	})
}

func (value *MatrixValue) UnmarshalJSON(encoded []byte) error {
	var stored struct {
		Rows    int       `json:"rows"`
		Columns int       `json:"columns"`
		Values  []float64 `json:"values"`
	}
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return err
	}
	validated, err := NewMatrixValue(stored.Rows, stored.Columns, stored.Values)
	if err != nil {
		return err
	}
	*value = validated
	return nil
}

type ChannelNames struct {
	names []string
}

func NewChannelNames(names []string) (ChannelNames, error) {
	if len(names) == 0 {
		return ChannelNames{}, invalid("channel names must contain at least one name")
	}
	copied := make([]string, len(names))
	seen := make(map[string]struct{}, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return ChannelNames{}, invalid("channel name %d is empty", i+1)
		}
		if _, exists := seen[name]; exists {
			return ChannelNames{}, invalid("channel name %q is duplicated", name)
		}
		seen[name] = struct{}{}
		copied[i] = name
	}
	return ChannelNames{names: copied}, nil
}

func ParseChannelNames(raw string) (ChannelNames, error) {
	normalized := strings.NewReplacer(";", ",", "\r\n", ",", "\n", ",").Replace(raw)
	parts := strings.Split(normalized, ",")
	return NewChannelNames(parts)
}

func (value ChannelNames) Len() int {
	return len(value.names)
}

func (value ChannelNames) Names() []string {
	return append([]string(nil), value.names...)
}

func (value ChannelNames) Text() string {
	return strings.Join(value.names, ", ")
}

func (value ChannelNames) MarshalJSON() ([]byte, error) {
	return json.Marshal(value.names)
}

func (value *ChannelNames) UnmarshalJSON(encoded []byte) error {
	var names []string
	if err := json.Unmarshal(encoded, &names); err != nil {
		return err
	}
	validated, err := NewChannelNames(names)
	if err != nil {
		return err
	}
	*value = validated
	return nil
}

type PolynomialMatrixValue struct {
	rows, columns int
	values        [][][]float64
}

func NewPolynomialMatrixValue(values [][][]float64) (PolynomialMatrixValue, error) {
	if len(values) == 0 || len(values[0]) == 0 {
		return PolynomialMatrixValue{}, invalid("polynomial matrix must have positive dimensions")
	}
	rows, columns := len(values), len(values[0])
	copied := make([][][]float64, rows)
	for row := range rows {
		if len(values[row]) != columns {
			return PolynomialMatrixValue{}, invalid(
				"polynomial matrix row %d has %d columns; expected %d",
				row+1, len(values[row]), columns,
			)
		}
		copied[row] = make([][]float64, columns)
		for column := range columns {
			if len(values[row][column]) == 0 {
				return PolynomialMatrixValue{}, invalid(
					"polynomial at row %d column %d is empty", row+1, column+1,
				)
			}
			copied[row][column] = append([]float64(nil), values[row][column]...)
			for _, coefficient := range copied[row][column] {
				if math.IsNaN(coefficient) || math.IsInf(coefficient, 0) {
					return PolynomialMatrixValue{}, invalid("polynomial coefficients must be finite")
				}
			}
		}
	}
	return PolynomialMatrixValue{rows: rows, columns: columns, values: copied}, nil
}

func ParsePolynomialMatrixValue(raw string) (PolynomialMatrixValue, error) {
	rowsText := nonemptyLines(raw)
	if len(rowsText) == 0 {
		return PolynomialMatrixValue{}, invalid("polynomial matrix must contain at least one row")
	}
	values := make([][][]float64, len(rowsText))
	columns := -1
	for row, rowText := range rowsText {
		cells := strings.Split(rowText, "|")
		if columns < 0 {
			columns = len(cells)
		} else if len(cells) != columns {
			return PolynomialMatrixValue{}, invalid(
				"polynomial matrix row %d has %d columns; expected %d",
				row+1, len(cells), columns,
			)
		}
		values[row] = make([][]float64, len(cells))
		for column, cell := range cells {
			coefficients, err := parseNumericRow(cell)
			if err != nil {
				return PolynomialMatrixValue{}, invalid(
					"polynomial at row %d column %d: %s", row+1, column+1, err,
				)
			}
			values[row][column] = coefficients
		}
	}
	return NewPolynomialMatrixValue(values)
}

func (value PolynomialMatrixValue) Dims() (rows, columns int) {
	return value.rows, value.columns
}

func (value PolynomialMatrixValue) Values() [][][]float64 {
	copied := make([][][]float64, value.rows)
	for row := range value.rows {
		copied[row] = make([][]float64, value.columns)
		for column := range value.columns {
			copied[row][column] = append([]float64(nil), value.values[row][column]...)
		}
	}
	return copied
}

func (value PolynomialMatrixValue) Text() string {
	rows := make([]string, value.rows)
	for row := range value.rows {
		cells := make([]string, value.columns)
		for column := range value.columns {
			cells[column] = numericRowText(value.values[row][column])
		}
		rows[row] = strings.Join(cells, " | ")
	}
	return strings.Join(rows, "\n")
}

func (value PolynomialMatrixValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(value.values)
}

func (value *PolynomialMatrixValue) UnmarshalJSON(encoded []byte) error {
	var values [][][]float64
	if err := json.Unmarshal(encoded, &values); err != nil {
		return err
	}
	validated, err := NewPolynomialMatrixValue(values)
	if err != nil {
		return err
	}
	*value = validated
	return nil
}

type ComplexRootMatrixValue struct {
	rows, columns int
	values        [][][]complex128
}

func NewComplexRootMatrixValue(values [][][]complex128) (ComplexRootMatrixValue, error) {
	if len(values) == 0 || len(values[0]) == 0 {
		return ComplexRootMatrixValue{}, invalid("root matrix must have positive dimensions")
	}
	rows, columns := len(values), len(values[0])
	copied := make([][][]complex128, rows)
	for row := range rows {
		if len(values[row]) != columns {
			return ComplexRootMatrixValue{}, invalid(
				"root matrix row %d has %d columns; expected %d",
				row+1, len(values[row]), columns,
			)
		}
		copied[row] = make([][]complex128, columns)
		for column := range columns {
			copied[row][column] = append([]complex128(nil), values[row][column]...)
			for _, root := range copied[row][column] {
				if !finiteComplex(root) {
					return ComplexRootMatrixValue{}, invalid("roots must be finite")
				}
			}
		}
	}
	return ComplexRootMatrixValue{rows: rows, columns: columns, values: copied}, nil
}

func ParseComplexRootMatrixValue(raw string) (ComplexRootMatrixValue, error) {
	rowsText := nonemptyLines(raw)
	if len(rowsText) == 0 {
		return ComplexRootMatrixValue{}, invalid("root matrix must contain at least one row")
	}
	values := make([][][]complex128, len(rowsText))
	columns := -1
	for row, rowText := range rowsText {
		cells := strings.Split(rowText, "|")
		if columns < 0 {
			columns = len(cells)
		} else if len(cells) != columns {
			return ComplexRootMatrixValue{}, invalid(
				"root matrix row %d has %d columns; expected %d",
				row+1, len(cells), columns,
			)
		}
		values[row] = make([][]complex128, len(cells))
		for column, cell := range cells {
			roots, err := parseComplexList(cell, true)
			if err != nil {
				return ComplexRootMatrixValue{}, invalid(
					"roots at row %d column %d: %s", row+1, column+1, err,
				)
			}
			values[row][column] = roots
		}
	}
	return NewComplexRootMatrixValue(values)
}

func (value ComplexRootMatrixValue) Dims() (rows, columns int) {
	return value.rows, value.columns
}

func (value ComplexRootMatrixValue) Values() [][][]complex128 {
	copied := make([][][]complex128, value.rows)
	for row := range value.rows {
		copied[row] = make([][]complex128, value.columns)
		for column := range value.columns {
			copied[row][column] = append([]complex128(nil), value.values[row][column]...)
		}
	}
	return copied
}

func (value ComplexRootMatrixValue) Text() string {
	rows := make([]string, value.rows)
	for row := range value.rows {
		cells := make([]string, value.columns)
		for column := range value.columns {
			if len(value.values[row][column]) == 0 {
				cells[column] = "-"
				continue
			}
			roots := make([]string, len(value.values[row][column]))
			for index, root := range value.values[row][column] {
				roots[index] = complexText(root)
			}
			cells[column] = strings.Join(roots, ", ")
		}
		rows[row] = strings.Join(cells, " | ")
	}
	return strings.Join(rows, "\n")
}

func (value ComplexRootMatrixValue) MarshalJSON() ([]byte, error) {
	stored := make([][][][2]float64, value.rows)
	for row := range value.rows {
		stored[row] = make([][][2]float64, value.columns)
		for column := range value.columns {
			stored[row][column] = make([][2]float64, len(value.values[row][column]))
			for index, root := range value.values[row][column] {
				stored[row][column][index] = [2]float64{real(root), imag(root)}
			}
		}
	}
	return json.Marshal(stored)
}

func (value *ComplexRootMatrixValue) UnmarshalJSON(encoded []byte) error {
	var stored [][][][2]float64
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return err
	}
	values := make([][][]complex128, len(stored))
	for row := range stored {
		values[row] = make([][]complex128, len(stored[row]))
		for column := range stored[row] {
			values[row][column] = make([]complex128, len(stored[row][column]))
			for index, root := range stored[row][column] {
				values[row][column][index] = complex(root[0], root[1])
			}
		}
	}
	validated, err := NewComplexRootMatrixValue(values)
	if err != nil {
		return err
	}
	*value = validated
	return nil
}

type ComplexResponseValue struct {
	samples, outputs, inputs int
	values                   []complex128
}

func NewComplexResponseValue(
	samples, outputs, inputs int,
	values []complex128,
) (ComplexResponseValue, error) {
	if samples <= 0 || outputs <= 0 || inputs <= 0 {
		return ComplexResponseValue{}, invalid("frequency response dimensions must be positive")
	}
	if len(values) != samples*outputs*inputs {
		return ComplexResponseValue{}, invalid(
			"frequency response has %d values for %d samples by %d outputs by %d inputs",
			len(values), samples, outputs, inputs,
		)
	}
	copied := append([]complex128(nil), values...)
	for _, response := range copied {
		if !finiteComplex(response) {
			return ComplexResponseValue{}, invalid("frequency responses must be finite")
		}
	}
	return ComplexResponseValue{
		samples: samples, outputs: outputs, inputs: inputs, values: copied,
	}, nil
}

func ParseComplexResponseValue(raw string, outputs, inputs int) (ComplexResponseValue, error) {
	lines := nonemptyLines(raw)
	if len(lines) == 0 {
		return ComplexResponseValue{}, invalid("frequency response must contain at least one sample")
	}
	values := make([]complex128, 0, len(lines)*outputs*inputs)
	expected := outputs * inputs
	for sample, line := range lines {
		cells := strings.Split(line, "|")
		if len(cells) != expected {
			return ComplexResponseValue{}, invalid(
				"frequency response sample %d has %d channels; expected %d",
				sample+1, len(cells), expected,
			)
		}
		for channel, cell := range cells {
			response, err := parseComplex(strings.TrimSpace(cell))
			if err != nil {
				return ComplexResponseValue{}, invalid(
					"frequency response sample %d channel %d: %s",
					sample+1, channel+1, err,
				)
			}
			values = append(values, response)
		}
	}
	return NewComplexResponseValue(len(lines), outputs, inputs, values)
}

func (value ComplexResponseValue) Dims() (samples, outputs, inputs int) {
	return value.samples, value.outputs, value.inputs
}

func (value ComplexResponseValue) Values() []complex128 {
	return append([]complex128(nil), value.values...)
}

func (value ComplexResponseValue) Tensor() [][][]complex128 {
	response := make([][][]complex128, value.samples)
	for sample := range value.samples {
		response[sample] = make([][]complex128, value.outputs)
		for output := range value.outputs {
			start := (sample*value.outputs + output) * value.inputs
			response[sample][output] = append(
				[]complex128(nil), value.values[start:start+value.inputs]...,
			)
		}
	}
	return response
}

func (value ComplexResponseValue) Text() string {
	lines := make([]string, value.samples)
	width := value.outputs * value.inputs
	for sample := range value.samples {
		cells := make([]string, width)
		for channel := range width {
			cells[channel] = complexText(value.values[sample*width+channel])
		}
		lines[sample] = strings.Join(cells, " | ")
	}
	return strings.Join(lines, "\n")
}

func (value ComplexResponseValue) MarshalJSON() ([]byte, error) {
	stored := make([][2]float64, len(value.values))
	for index, response := range value.values {
		stored[index] = [2]float64{real(response), imag(response)}
	}
	return json.Marshal(struct {
		Samples int          `json:"samples"`
		Outputs int          `json:"outputs"`
		Inputs  int          `json:"inputs"`
		Values  [][2]float64 `json:"values"`
	}{
		Samples: value.samples, Outputs: value.outputs, Inputs: value.inputs, Values: stored,
	})
}

func (value *ComplexResponseValue) UnmarshalJSON(encoded []byte) error {
	var stored struct {
		Samples int          `json:"samples"`
		Outputs int          `json:"outputs"`
		Inputs  int          `json:"inputs"`
		Values  [][2]float64 `json:"values"`
	}
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return err
	}
	values := make([]complex128, len(stored.Values))
	for index, response := range stored.Values {
		values[index] = complex(response[0], response[1])
	}
	validated, err := NewComplexResponseValue(
		stored.Samples, stored.Outputs, stored.Inputs, values,
	)
	if err != nil {
		return err
	}
	*value = validated
	return nil
}

func nonemptyLines(raw string) []string {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func parseComplexList(raw string, allowEmpty bool) ([]complex128, error) {
	raw = strings.TrimSpace(raw)
	if allowEmpty && (raw == "" || raw == "-") {
		return nil, nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	if len(parts) == 0 {
		return nil, invalid("complex value list is empty")
	}
	values := make([]complex128, len(parts))
	for index, part := range parts {
		value, err := parseComplex(part)
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	return values, nil
}

func parseComplex(raw string) (complex128, error) {
	raw = strings.Trim(strings.TrimSpace(raw), "()")
	value, err := strconv.ParseComplex(raw, 128)
	if err != nil || !finiteComplex(value) {
		return 0, invalid("value %q must be a finite real or a+bi number", raw)
	}
	return value, nil
}

func finiteComplex(value complex128) bool {
	return !math.IsNaN(real(value)) && !math.IsInf(real(value), 0) &&
		!math.IsNaN(imag(value)) && !math.IsInf(imag(value), 0)
}

func complexText(value complex128) string {
	if imag(value) == 0 {
		return strconv.FormatFloat(real(value), 'g', -1, 64)
	}
	sign := "+"
	imaginary := imag(value)
	if imaginary < 0 {
		sign = "-"
		imaginary = -imaginary
	}
	return strconv.FormatFloat(real(value), 'g', -1, 64) + sign +
		strconv.FormatFloat(imaginary, 'g', -1, 64) + "i"
}

func parseNumericRow(raw string) ([]float64, error) {
	parts := strings.FieldsFunc(strings.TrimSpace(raw), func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	if len(parts) == 0 {
		return nil, invalid("value list must contain at least one number")
	}
	values := make([]float64, len(parts))
	for i, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return nil, invalid("value %q is not a number", part)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, invalid("values must be finite")
		}
		values[i] = value
	}
	return values, nil
}

func numericRowText(values []float64) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.FormatFloat(value, 'g', -1, 64)
	}
	return strings.Join(parts, ", ")
}

func cloneMatrixValue(value *MatrixValue) *MatrixValue {
	if value == nil {
		return nil
	}
	cloned, err := NewMatrixValue(value.rows, value.columns, value.values)
	if err != nil {
		panic(fmt.Sprintf("clone validated matrix: %v", err))
	}
	return &cloned
}

func cloneVectorValue(value *VectorValue) *VectorValue {
	if value == nil {
		return nil
	}
	cloned, err := NewVectorValue(value.values)
	if err != nil {
		panic(fmt.Sprintf("clone validated vector: %v", err))
	}
	return &cloned
}

func cloneChannelNames(value *ChannelNames) *ChannelNames {
	if value == nil {
		return nil
	}
	cloned, err := NewChannelNames(value.names)
	if err != nil {
		panic(fmt.Sprintf("clone validated channel names: %v", err))
	}
	return &cloned
}

func clonePolynomialMatrixValue(value *PolynomialMatrixValue) *PolynomialMatrixValue {
	if value == nil {
		return nil
	}
	cloned, err := NewPolynomialMatrixValue(value.values)
	if err != nil {
		panic(fmt.Sprintf("clone validated polynomial matrix: %v", err))
	}
	return &cloned
}

func cloneComplexRootMatrixValue(value *ComplexRootMatrixValue) *ComplexRootMatrixValue {
	if value == nil {
		return nil
	}
	cloned, err := NewComplexRootMatrixValue(value.values)
	if err != nil {
		panic(fmt.Sprintf("clone validated root matrix: %v", err))
	}
	return &cloned
}

func cloneComplexResponseValue(value *ComplexResponseValue) *ComplexResponseValue {
	if value == nil {
		return nil
	}
	cloned, err := NewComplexResponseValue(
		value.samples, value.outputs, value.inputs, value.values,
	)
	if err != nil {
		panic(fmt.Sprintf("clone validated frequency response: %v", err))
	}
	return &cloned
}
