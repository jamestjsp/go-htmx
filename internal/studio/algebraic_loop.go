package studio

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jamestjsp/controlsys"
)

const algebraicLoopFallback = "flowsheet contains an unsolvable algebraic loop; add dynamics or change a direct-feedthrough gain"

func algebraicLoopMessage(
	err error,
	signals []compiledSignal,
	blocks map[int64]Block,
) string {
	var diagnostic *controlsys.AlgebraicLoopError
	if !errors.As(err, &diagnostic) || len(diagnostic.Signals) == 0 {
		return algebraicLoopFallback
	}

	compiledByName := make(map[string]compiledSignal, len(signals))
	for _, signal := range signals {
		compiledByName[signal.Name] = signal
	}

	participants := make([]string, 0, len(diagnostic.Signals))
	seen := make(map[string]struct{}, len(diagnostic.Signals))
	for _, name := range diagnostic.Signals {
		signal, ok := compiledByName[name]
		if !ok {
			continue
		}
		block, ok := blocks[signal.BlockID]
		if !ok {
			continue
		}
		label := algebraicLoopSignalLabel(block, signal)
		if _, duplicate := seen[label]; duplicate {
			continue
		}
		seen[label] = struct{}{}
		participants = append(participants, label)
	}
	if len(participants) == 0 {
		return algebraicLoopFallback
	}

	condition := "the direct-feedthrough equation is numerically singular"
	if math.IsInf(diagnostic.Condition, 1) {
		condition = "the direct-feedthrough equation is exactly singular"
	} else if !math.IsNaN(diagnostic.Condition) && diagnostic.Condition > 0 {
		condition = fmt.Sprintf(
			"the direct-feedthrough equation is numerically singular (condition number %.3g)",
			diagnostic.Condition,
		)
	}

	return fmt.Sprintf(
		"flowsheet contains an unsolvable algebraic loop involving %s; %s; add dynamics or change a direct-feedthrough gain",
		strings.Join(participants, ", "),
		condition,
	)
}

func algebraicLoopSignalLabel(block Block, signal compiledSignal) string {
	direction := "input"
	if signal.Role == compiledBlockOutput {
		direction = "output"
	}
	label := fmt.Sprintf("%q %s port %d", block.Name, direction, signal.Port+1)
	if signal.Width > 1 && signal.ChannelName != "" {
		label += fmt.Sprintf(" channel %q", signal.ChannelName)
	}
	return label
}
