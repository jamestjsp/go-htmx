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
// afterSwap still exposes the outgoing node to querySelector, so rebuilding
// there writes client state to markup htmx is about to discard. afterSettle
// is the first event where every step can measure and update the live node.
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

function reapply() {
  steps.forEach((step) => step())
}

document.addEventListener('htmx:beforeSwap', () => beforeSwapSteps.forEach((step) => step()))
document.addEventListener('htmx:afterSettle', reapply)
document.addEventListener('htmx:historyRestore', reapply)
