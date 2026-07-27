// =====================================================================
// The handful of elements every canvas module talks to, plus the writers
// for the strings they report through.
//
// These are functions rather than cached nodes because htmx replaces
// #workbench wholesale on nearly every action: a node captured at load is
// detached by the first edit, and writing to it would be silently lost.
// =====================================================================

export const workbench = () => document.querySelector('#workbench')
export const canvas = () => document.querySelector('#flow-canvas')
export const sheet = () => document.querySelector('#sheet')

// The status line and the canvas hint are server-rendered and therefore
// absent until the workbench is on the page; every write is conditional.
export function setStatus(text) {
  const node = document.querySelector('#interaction-status')
  if (node) node.textContent = text
}

export function setHint(text) {
  const node = document.querySelector('#canvas-hint')
  if (node) node.textContent = text
}

// The readout rail is server-rendered markup with stable ids; JS only
// fills in the values that change as the pointer and the selection move.
export function setReadout(selector, text) {
  const node = document.querySelector(selector)
  if (node && node.textContent !== text) node.textContent = text
}
