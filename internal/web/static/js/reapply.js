// =====================================================================
// The htmx re-apply contract.
//
// Nearly every action swaps the whole #workbench fragment away, taking
// the pan and zoom transform, the selected classes, the drawn wires and
// the rail and dock attributes with it. All of that is client state, so
// the only way it survives an edit is to be put back afterwards.
//
// One entry point, one ordered list of steps. Modules register what they
// need to rebuild instead of each adding its own htmx:afterSettle
// listener, so the order the state is rebuilt in is written down in one
// place (main.js) rather than emerging from script order.
//
// Both swap events are needed: at afterSwap the replacement node is not
// yet the one querySelector returns, so styling there alone writes to a
// node htmx is about to discard. afterSettle is what actually sticks.
// Steps are told which one they are running for, because a step that
// measures the new markup can only trust the settled pass.
//
// Back and Forward restore the page from htmx's history cache, which
// fires neither swap event, so historyRestore runs the settled pass too.
// =====================================================================

const steps = []
const beforeSwapSteps = []

export function onReapply(step) {
  steps.push(step)
}

export function onBeforeSwap(step) {
  beforeSwapSteps.push(step)
}

function reapply(settled) {
  steps.forEach((step) => step({ settled }))
}

document.addEventListener('htmx:beforeSwap', () => beforeSwapSteps.forEach((step) => step()))
document.addEventListener('htmx:afterSwap', () => reapply(false))
document.addEventListener('htmx:afterSettle', () => reapply(true))
document.addEventListener('htmx:historyRestore', () => reapply(true))
