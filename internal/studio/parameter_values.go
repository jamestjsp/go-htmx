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
