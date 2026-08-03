package studio

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"sort"
	"strconv"
	"strings"

	"gonum.org/v1/gonum/mat"
)

const (
	maxNonlinearExpressionDepth = 64
	sqrtMachineEpsilon          = 1.4901161193847656e-08
)

type nonlinearExpression struct {
	source    string
	tree      ast.Expr
	stateRefs map[string]struct{}
	inputRefs map[string]struct{}
}

type nonlinearProgram struct {
	dynamics []nonlinearExpression
	outputs  []nonlinearExpression
	states   map[string]int
	inputs   map[string]int
}

type nonlinearRuntime struct {
	dynamics                   func(*mat.VecDense, *mat.VecDense) *mat.VecDense
	output                     func(*mat.VecDense, *mat.VecDense) *mat.VecDense
	transition                 func(*mat.VecDense, *mat.VecDense) *mat.VecDense
	measurement                func(*mat.VecDense) *mat.VecDense
	transitionJacobian         func(*mat.VecDense, *mat.VecDense) *mat.Dense
	measurementJacobian        func(*mat.VecDense) *mat.Dense
	measurementInputReferences map[string][]string
}

func compileNonlinearDefinition(definition NonlinearDefinition) (nonlinearProgram, error) {
	if len(definition.Dynamics) != len(definition.StateNames) {
		return nonlinearProgram{}, invalid(
			"nonlinear definition has %d dynamics expressions; want %d",
			len(definition.Dynamics), len(definition.StateNames),
		)
	}
	if len(definition.Outputs) != len(definition.OutputNames) {
		return nonlinearProgram{}, invalid(
			"nonlinear definition has %d output expressions; want %d",
			len(definition.Outputs), len(definition.OutputNames),
		)
	}
	if math.IsNaN(definition.SampleTime) || math.IsInf(definition.SampleTime, 0) || definition.SampleTime < 0 {
		return nonlinearProgram{}, invalid("nonlinear sampleTime must be a non-negative finite number")
	}
	if definition.IntegrationSteps < 0 {
		return nonlinearProgram{}, invalid("nonlinear integrationSteps must be non-negative")
	}

	states := make(map[string]int, len(definition.StateNames))
	for index, name := range definition.StateNames {
		states[name] = index
	}
	inputs := make(map[string]int, len(definition.InputNames))
	for index, name := range definition.InputNames {
		if _, exists := states[name]; exists {
			return nonlinearProgram{}, invalid("nonlinear signal name %q is declared as both state and input", name)
		}
		inputs[name] = index
	}
	declared := make(map[string]struct{}, len(states)+len(inputs))
	for name := range states {
		declared[name] = struct{}{}
	}
	for name := range inputs {
		declared[name] = struct{}{}
	}

	program := nonlinearProgram{states: states, inputs: inputs}
	program.dynamics = make([]nonlinearExpression, len(definition.Dynamics))
	for index, source := range definition.Dynamics {
		expression, err := parseNonlinearExpression(
			"dynamics", index, source, declared, states, inputs,
		)
		if err != nil {
			return nonlinearProgram{}, err
		}
		program.dynamics[index] = expression
	}
	program.outputs = make([]nonlinearExpression, len(definition.Outputs))
	for index, source := range definition.Outputs {
		expression, err := parseNonlinearExpression(
			"output", index, source, declared, states, inputs,
		)
		if err != nil {
			return nonlinearProgram{}, err
		}
		program.outputs[index] = expression
	}
	return program, nil
}

func parseNonlinearExpression(
	role string,
	index int,
	source string,
	declared map[string]struct{},
	states, inputs map[string]int,
) (nonlinearExpression, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nonlinearExpression{}, invalid("nonlinear %s expression %d is empty", role, index+1)
	}
	tree, err := parser.ParseExpr(source)
	if err != nil {
		return nonlinearExpression{}, invalid(
			"nonlinear %s expression %d %q is invalid: %v", role, index+1, source, err,
		)
	}
	expression := nonlinearExpression{
		source: source, tree: tree,
		stateRefs: make(map[string]struct{}), inputRefs: make(map[string]struct{}),
	}
	if err := validateNonlinearExpression(
		tree, source, role, index, 1, maxNonlinearExpressionDepth,
		declared, states, inputs, &expression,
	); err != nil {
		return nonlinearExpression{}, err
	}
	return expression, nil
}

func validateNonlinearExpression(
	expression ast.Expr,
	source, role string,
	index, depth, maxDepth int,
	declared map[string]struct{},
	states, inputs map[string]int,
	parsed *nonlinearExpression,
) error {
	if depth > maxDepth {
		return invalid(
			"nonlinear %s expression %d %q exceeds maximum depth %d",
			role, index+1, source, maxDepth,
		)
	}
	child := func(node ast.Expr) error {
		return validateNonlinearExpression(
			node, source, role, index, depth+1, maxDepth,
			declared, states, inputs, parsed,
		)
	}
	switch node := expression.(type) {
	case *ast.BasicLit:
		if node.Kind != token.INT && node.Kind != token.FLOAT {
			return invalid(
				"nonlinear %s expression %d %q uses unsupported literal %q",
				role, index+1, source, node.Value,
			)
		}
		if _, err := strconv.ParseFloat(node.Value, 64); err != nil {
			return invalid(
				"nonlinear %s expression %d %q uses invalid numeric literal %q",
				role, index+1, source, node.Value,
			)
		}
	case *ast.Ident:
		if node.Name == "pi" || node.Name == "e" {
			return nil
		}
		if _, ok := declared[node.Name]; !ok {
			return invalid(
				"nonlinear %s expression %d %q references unknown signal %q; declared signals: %s",
				role, index+1, source, node.Name, declaredSignalList(states, inputs),
			)
		}
		if _, ok := states[node.Name]; ok {
			parsed.stateRefs[node.Name] = struct{}{}
		}
		if _, ok := inputs[node.Name]; ok {
			parsed.inputRefs[node.Name] = struct{}{}
		}
	case *ast.ParenExpr:
		return child(node.X)
	case *ast.UnaryExpr:
		if node.Op != token.ADD && node.Op != token.SUB {
			return invalid(
				"nonlinear %s expression %d %q uses unsupported unary operator %q",
				role, index+1, source, node.Op,
			)
		}
		return child(node.X)
	case *ast.BinaryExpr:
		if node.Op == token.XOR {
			return invalid(
				"nonlinear %s expression %d %q uses ^; use pow(x, 2) for powers",
				role, index+1, source,
			)
		}
		if node.Op != token.ADD && node.Op != token.SUB &&
			node.Op != token.MUL && node.Op != token.QUO {
			return invalid(
				"nonlinear %s expression %d %q uses unsupported operator %q",
				role, index+1, source, node.Op,
			)
		}
		if err := child(node.X); err != nil {
			return err
		}
		return child(node.Y)
	case *ast.CallExpr:
		function, ok := node.Fun.(*ast.Ident)
		if !ok {
			return invalid(
				"nonlinear %s expression %d %q allows only direct calls to approved functions",
				role, index+1, source,
			)
		}
		if !nonlinearFunctionArity(function.Name, len(node.Args)) {
			return invalid(
				"nonlinear %s expression %d %q uses unsupported function %q with %d arguments",
				role, index+1, source, function.Name, len(node.Args),
			)
		}
		for _, argument := range node.Args {
			if err := child(argument); err != nil {
				return err
			}
		}
	default:
		return invalid(
			"nonlinear %s expression %d %q uses unsupported syntax %T",
			role, index+1, source, expression,
		)
	}
	return nil
}

func nonlinearFunctionArity(name string, count int) bool {
	switch name {
	case "sin", "cos", "tan", "asin", "acos", "atan", "sinh", "cosh", "tanh", "exp", "log", "log10", "sqrt", "abs":
		return count == 1
	case "atan2", "pow":
		return count == 2
	case "min", "max":
		return count >= 2
	default:
		return false
	}
}

func declaredSignalList(states, inputs map[string]int) string {
	names := make([]string, 0, len(states)+len(inputs))
	for name := range states {
		names = append(names, name)
	}
	for name := range inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "<none>"
	}
	return strings.Join(names, ", ")
}

func (expression nonlinearExpression) evaluate(state, input *mat.VecDense, states, inputs map[string]int) (float64, error) {
	return evaluateNonlinearAST(expression.tree, state, input, states, inputs)
}

func evaluateNonlinearAST(
	expression ast.Expr,
	state, input *mat.VecDense,
	states, inputs map[string]int,
) (float64, error) {
	switch node := expression.(type) {
	case *ast.BasicLit:
		return strconv.ParseFloat(node.Value, 64)
	case *ast.Ident:
		switch node.Name {
		case "pi":
			return math.Pi, nil
		case "e":
			return math.E, nil
		}
		if index, ok := states[node.Name]; ok {
			return state.AtVec(index), nil
		}
		if index, ok := inputs[node.Name]; ok {
			return input.AtVec(index), nil
		}
		return 0, invalid("nonlinear evaluator encountered unknown signal %q", node.Name)
	case *ast.ParenExpr:
		return evaluateNonlinearAST(node.X, state, input, states, inputs)
	case *ast.UnaryExpr:
		value, err := evaluateNonlinearAST(node.X, state, input, states, inputs)
		if err != nil {
			return 0, err
		}
		if node.Op == token.ADD {
			return value, nil
		}
		return -value, nil
	case *ast.BinaryExpr:
		left, err := evaluateNonlinearAST(node.X, state, input, states, inputs)
		if err != nil {
			return 0, err
		}
		right, err := evaluateNonlinearAST(node.Y, state, input, states, inputs)
		if err != nil {
			return 0, err
		}
		switch node.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			return left / right, nil
		}
	case *ast.CallExpr:
		values := make([]float64, len(node.Args))
		for index, argument := range node.Args {
			value, err := evaluateNonlinearAST(argument, state, input, states, inputs)
			if err != nil {
				return 0, err
			}
			values[index] = value
		}
		return evaluateNonlinearFunction(node.Fun.(*ast.Ident).Name, values), nil
	}
	return 0, invalid("nonlinear evaluator encountered unsupported syntax")
}

func evaluateNonlinearFunction(name string, values []float64) float64 {
	switch name {
	case "sin":
		return math.Sin(values[0])
	case "cos":
		return math.Cos(values[0])
	case "tan":
		return math.Tan(values[0])
	case "asin":
		return math.Asin(values[0])
	case "acos":
		return math.Acos(values[0])
	case "atan":
		return math.Atan(values[0])
	case "atan2":
		return math.Atan2(values[0], values[1])
	case "sinh":
		return math.Sinh(values[0])
	case "cosh":
		return math.Cosh(values[0])
	case "tanh":
		return math.Tanh(values[0])
	case "exp":
		return math.Exp(values[0])
	case "log":
		return math.Log(values[0])
	case "log10":
		return math.Log10(values[0])
	case "sqrt":
		return math.Sqrt(values[0])
	case "abs":
		return math.Abs(values[0])
	case "pow":
		return math.Pow(values[0], values[1])
	case "min":
		result := values[0]
		for _, value := range values[1:] {
			result = math.Min(result, value)
		}
		return result
	case "max":
		result := values[0]
		for _, value := range values[1:] {
			result = math.Max(result, value)
		}
		return result
	}
	return math.NaN()
}

func (program nonlinearProgram) runtime(definition NonlinearDefinition) nonlinearRuntime {
	evaluate := func(expressions []nonlinearExpression, state, input *mat.VecDense) *mat.VecDense {
		values := make([]float64, len(expressions))
		for index, expression := range expressions {
			value, err := expression.evaluate(state, input, program.states, program.inputs)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				return nil
			}
			values[index] = value
		}
		return mat.NewVecDense(len(values), values)
	}
	dynamics := func(state, input *mat.VecDense) *mat.VecDense {
		return evaluate(program.dynamics, state, input)
	}
	output := func(state, input *mat.VecDense) *mat.VecDense {
		return evaluate(program.outputs, state, input)
	}
	steps := definition.IntegrationSteps
	if steps == 0 {
		steps = 1
	}
	transition := func(state, input *mat.VecDense) *mat.VecDense {
		if definition.SampleTime <= 0 {
			return nil
		}
		result := integrateNonlinearRK4(dynamics, state, input, definition.SampleTime, steps)
		if !finiteNonlinearVector(result) {
			return nil
		}
		return result
	}
	measurement := func(state *mat.VecDense) *mat.VecDense {
		input := mat.NewVecDense(len(definition.InputNames), nil)
		return output(state, input)
	}
	transitionJacobian := func(state, input *mat.VecDense) *mat.Dense {
		result := finiteDifferenceStateJacobian(transition, state, input)
		if !finiteNonlinearMatrix(result) {
			return nil
		}
		return result
	}
	measurementJacobian := func(state *mat.VecDense) *mat.Dense {
		result := finiteDifferenceMeasurementJacobian(measurement, state)
		if !finiteNonlinearMatrix(result) {
			return nil
		}
		return result
	}
	inputReferences := make(map[string][]string, len(program.outputs))
	for index, expression := range program.outputs {
		for name := range expression.inputRefs {
			inputReferences[definition.OutputNames[index]] = append(
				inputReferences[definition.OutputNames[index]], name,
			)
		}
	}
	for outputName := range inputReferences {
		sort.Strings(inputReferences[outputName])
	}
	return nonlinearRuntime{
		dynamics: dynamics, output: output, transition: transition,
		measurement: measurement, transitionJacobian: transitionJacobian,
		measurementJacobian:        measurementJacobian,
		measurementInputReferences: inputReferences,
	}
}

func integrateNonlinearRK4(
	dynamics func(*mat.VecDense, *mat.VecDense) *mat.VecDense,
	state, input *mat.VecDense,
	sampleTime float64, steps int,
) *mat.VecDense {
	dt := sampleTime / float64(steps)
	current := mat.VecDenseCopyOf(state)
	for range steps {
		k1 := dynamics(current, input)
		if k1 == nil {
			return nil
		}
		k2 := dynamics(rk4State(current, k1, dt/2), input)
		if k2 == nil {
			return nil
		}
		k3 := dynamics(rk4State(current, k2, dt/2), input)
		if k3 == nil {
			return nil
		}
		k4 := dynamics(rk4State(current, k3, dt), input)
		if k4 == nil {
			return nil
		}
		updated := mat.VecDenseCopyOf(current)
		var weighted mat.VecDense
		weighted.ScaleVec(1, k1)
		weighted.AddScaledVec(&weighted, 2, k2)
		weighted.AddScaledVec(&weighted, 2, k3)
		weighted.AddScaledVec(&weighted, 1, k4)
		updated.AddScaledVec(updated, dt/6, &weighted)
		current = updated
	}
	return current
}

func rk4State(state, derivative *mat.VecDense, scale float64) *mat.VecDense {
	result := mat.VecDenseCopyOf(state)
	result.AddScaledVec(result, scale, derivative)
	return result
}

func finiteNonlinearVector(vector *mat.VecDense) bool {
	if vector == nil {
		return false
	}
	for index := range vector.Len() {
		value := vector.AtVec(index)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func finiteNonlinearMatrix(matrix *mat.Dense) bool {
	if matrix == nil {
		return false
	}
	rows, columns := matrix.Dims()
	for row := range rows {
		for column := range columns {
			value := matrix.At(row, column)
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return false
			}
		}
	}
	return true
}

func finiteDifferenceStateJacobian(
	function func(*mat.VecDense, *mat.VecDense) *mat.VecDense,
	state, input *mat.VecDense,
) *mat.Dense {
	if state == nil || input == nil {
		return nil
	}
	base := function(state, input)
	if base == nil {
		return nil
	}
	rows, columns := base.Len(), state.Len()
	data := make([]float64, rows*columns)
	for column := range columns {
		h := sqrtMachineEpsilon * math.Max(math.Abs(state.AtVec(column)), 1)
		plus := mat.VecDenseCopyOf(state)
		minus := mat.VecDenseCopyOf(state)
		plus.SetVec(column, plus.AtVec(column)+h)
		minus.SetVec(column, minus.AtVec(column)-h)
		positive := function(plus, input)
		negative := function(minus, input)
		if positive == nil || negative == nil || positive.Len() != rows || negative.Len() != rows {
			return nil
		}
		for row := range rows {
			data[row*columns+column] = (positive.AtVec(row) - negative.AtVec(row)) / (2 * h)
		}
	}
	return mat.NewDense(rows, columns, data)
}

func finiteDifferenceMeasurementJacobian(
	function func(*mat.VecDense) *mat.VecDense,
	state *mat.VecDense,
) *mat.Dense {
	if state == nil {
		return nil
	}
	base := function(state)
	if base == nil {
		return nil
	}
	rows, columns := base.Len(), state.Len()
	data := make([]float64, rows*columns)
	for column := range columns {
		h := sqrtMachineEpsilon * math.Max(math.Abs(state.AtVec(column)), 1)
		plus := mat.VecDenseCopyOf(state)
		minus := mat.VecDenseCopyOf(state)
		plus.SetVec(column, plus.AtVec(column)+h)
		minus.SetVec(column, minus.AtVec(column)-h)
		positive := function(plus)
		negative := function(minus)
		if positive == nil || negative == nil || positive.Len() != rows || negative.Len() != rows {
			return nil
		}
		for row := range rows {
			data[row*columns+column] = (positive.AtVec(row) - negative.AtVec(row)) / (2 * h)
		}
	}
	return mat.NewDense(rows, columns, data)
}
