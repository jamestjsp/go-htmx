package studio

const defaultPIDFilterCoefficient = 100

func pidFilterCoefficientField() parameterDefinition {
	field := minimumNumberField(
		"filter_coefficient",
		"Filter coefficient N",
		"filter coefficient",
		"0.001",
		0.001,
		"1/sec",
		func(parameters *Parameters) *float64 {
			return &parameters.FilterCoefficient
		},
	)
	field.text = func(parameters Parameters) string {
		return formatFloat(pidFilterCoefficient(parameters))
	}
	field.bound.value = pidFilterCoefficient
	field.active = func(parameters Parameters) bool {
		return parameters.Derivative != 0
	}
	return field
}

func pidFilterTime(parameters Parameters) float64 {
	if parameters.FilterCoefficient > 0 {
		return 1 / parameters.FilterCoefficient
	}
	return parameters.FilterTime
}

func pidFilterCoefficient(parameters Parameters) float64 {
	if parameters.FilterCoefficient > 0 {
		return parameters.FilterCoefficient
	}
	if parameters.FilterTime > 0 {
		return 1 / parameters.FilterTime
	}
	return 0
}

func filterCoefficientFromTime(filterTime, fallback float64) float64 {
	if filterTime > 0 {
		return 1 / filterTime
	}
	return fallback
}
