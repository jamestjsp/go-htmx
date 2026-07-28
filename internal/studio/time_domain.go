package studio

import (
	"math"
)

type timeDomainKind string

const (
	timeDomainNeutral    timeDomainKind = "neutral"
	timeDomainContinuous timeDomainKind = "continuous"
	timeDomainDiscrete   timeDomainKind = "discrete"
)

type sampleTimeMode string

const (
	sampleTimeExplicit  sampleTimeMode = "explicit"
	sampleTimeInherited sampleTimeMode = "inherited"
)

type sampleTimeSpec struct {
	mode    sampleTimeMode
	seconds float64
}

func (spec sampleTimeSpec) resolve(baseStep float64) (float64, error) {
	switch spec.mode {
	case sampleTimeExplicit:
		if math.IsNaN(spec.seconds) || math.IsInf(spec.seconds, 0) || spec.seconds <= 0 {
			return 0, invalid("explicit sample time must be a positive finite number")
		}
		return spec.seconds, nil
	case sampleTimeInherited:
		if math.IsNaN(baseStep) || math.IsInf(baseStep, 0) || baseStep <= 0 {
			return 0, invalid("inherited sample time requires a positive run sample time")
		}
		return baseStep, nil
	default:
		return 0, invalid("sample time mode must be explicit or inherited")
	}
}

type blockTimeDomain struct {
	kind       timeDomainKind
	sampleTime sampleTimeSpec
}

func neutralTimeDomain() blockTimeDomain {
	return blockTimeDomain{kind: timeDomainNeutral}
}

func continuousTimeDomain() blockTimeDomain {
	return blockTimeDomain{kind: timeDomainContinuous}
}

func discreteTimeDomain(parameters Parameters) blockTimeDomain {
	return blockTimeDomain{
		kind: timeDomainDiscrete,
		sampleTime: sampleTimeSpec{
			mode:    normalizedSampleTimeMode(parameters),
			seconds: parameters.SampleTime,
		},
	}
}

func normalizedSampleTimeMode(parameters Parameters) sampleTimeMode {
	if parameters.SampleTimeMode == "" {
		return sampleTimeExplicit
	}
	return sampleTimeMode(parameters.SampleTimeMode)
}

type sampleTimeRelation uint8

const (
	sampleTimesEqual sampleTimeRelation = iota
	sampleTimesIntegerMultiple
	sampleTimesIncompatible
)

type sampleTimeCompatibility struct {
	relation sampleTimeRelation
	fast     float64
	slow     float64
	ratio    int
}

type sampleSchedule struct {
	updateEvery int
}

func (schedule sampleSchedule) updatesAt(sample int) bool {
	return sample%schedule.updateEvery == 0
}

func scheduleSampleTime(sampleTime, baseStep float64) (sampleSchedule, error) {
	if math.IsNaN(sampleTime) || math.IsInf(sampleTime, 0) || sampleTime <= 0 {
		return sampleSchedule{}, invalid("sample time must be a positive finite number")
	}
	if math.IsNaN(baseStep) || math.IsInf(baseStep, 0) || baseStep <= 0 {
		return sampleSchedule{}, invalid("run sample time must be a positive finite number")
	}
	samples := sampleTime / baseStep
	nearest := math.Round(samples)
	if nearest < 1 || math.Abs(samples-nearest) > 1e-9 {
		lowerSamples := math.Max(1, math.Floor(samples))
		upperSamples := math.Max(1, math.Ceil(samples))
		return sampleSchedule{}, invalid(
			"sample time %.12g s is not an integer multiple of run sample time %.12g s; use %.12g s or %.12g s",
			sampleTime, baseStep, lowerSamples*baseStep, upperSamples*baseStep,
		)
	}
	return sampleSchedule{updateEvery: int(nearest)}, nil
}

func compareSampleTimes(left, right float64) sampleTimeCompatibility {
	fast, slow := left, right
	if fast > slow {
		fast, slow = slow, fast
	}
	if math.Abs(slow-fast) <= 1e-9*math.Max(1, slow) {
		return sampleTimeCompatibility{
			relation: sampleTimesEqual, fast: fast, slow: slow, ratio: 1,
		}
	}
	ratio := slow / fast
	nearest := math.Round(ratio)
	if nearest >= 1 && math.Abs(ratio-nearest) <= 1e-9 {
		return sampleTimeCompatibility{
			relation: sampleTimesIntegerMultiple,
			fast:     fast,
			slow:     slow,
			ratio:    int(nearest),
		}
	}
	return sampleTimeCompatibility{
		relation: sampleTimesIncompatible, fast: fast, slow: slow,
	}
}
