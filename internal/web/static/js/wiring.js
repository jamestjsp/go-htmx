// =====================================================================
// Wiring a signal: the armed output port, the draft edge that tracks the
// pointer, and the two gestures that finish it — drag to an input, or
// click an output and then click an input.
// =====================================================================
import { canvas, setHint, setStatus, workbench } from './dom.js'
import { geometry, signalPath } from './geometry.js'
import { screenToSheet } from './viewport.js'

let connectionSource = null
let wiring = null

export const hasConnectionSource = () => connectionSource !== null

export function cancelConnection(message = 'Wire cancelled') {
  connectionSource = null
  wiring = null
  document.querySelectorAll('.hover-target, .hover-refused').forEach((node) => {
    node.classList.remove('hover-target', 'hover-refused')
  })
  document.body.classList.remove('is-connecting')
  document.querySelectorAll('.connecting-source').forEach((node) => node.classList.remove('connecting-source'))
  const draft = document.querySelector('#draft-edge')
  if (draft) draft.setAttribute('d', '')
  setStatus(message)
  setHint('Select an output port to start wiring')
}

export function beginConnection(button, event) {
  connectionSource = {
    id: button.dataset.outputBlock,
    port: button.dataset.outputPort,
    name: button.dataset.outputName,
    node: button.closest('.block-card'),
    button
  }
  document.body.classList.add('is-connecting')
  document.querySelectorAll('.connecting-source').forEach((node) => node.classList.remove('connecting-source'))
  connectionSource.node.classList.add('connecting-source')
  setStatus(`Wiring from ${connectionSource.name}; choose an input`)
  setHint(`Connecting from ${connectionSource.name}`)

  // Drag is the primary gesture. The pointer is captured on the canvas so
  // the draft edge keeps tracking even when it leaves the port, and the
  // target under the cursor is found geometrically.
  if (!event) return
  wiring = { pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, moved: false }
  const root = canvas()
  if (root) root.setPointerCapture(event.pointerId)
}

function portUnderPointer(event) {
  const element = document.elementFromPoint(event.clientX, event.clientY)
  return element ? element.closest('[data-input-port]') : null
}

// The server is the authority on what may connect; this only decides
// whether to show the target as inviting or as refused.
function targetIsValid(port) {
  if (!port || !connectionSource) return false
  return port.closest('.block-card') !== connectionSource.node
}

function highlightTarget(port) {
  document.querySelectorAll('.hover-target, .hover-refused').forEach((node) => {
    node.classList.remove('hover-target', 'hover-refused')
  })
  if (!port) return
  port.classList.add(targetIsValid(port) ? 'hover-target' : 'hover-refused')
}

export function moveWiring(event) {
  if (!wiring || event.pointerId !== wiring.pointerId) return
  if (Math.abs(event.clientX - wiring.startX) + Math.abs(event.clientY - wiring.startY) > 5) {
    wiring.moved = true
  }
  highlightTarget(portUnderPointer(event))
}

export function endWiring(event) {
  if (!wiring || event.pointerId !== wiring.pointerId) return
  const root = canvas()
  if (root && root.hasPointerCapture(event.pointerId)) root.releasePointerCapture(event.pointerId)
  const dragged = wiring.moved
  wiring = null
  highlightTarget(null)
  const port = portUnderPointer(event)
  if (port && targetIsValid(port)) {
    finishConnection(port)
    return
  }
  if (port && !targetIsValid(port)) {
    cancelConnection('A block cannot wire to itself')
    return
  }
  // A press with no travel leaves the sticky click-then-click mode
  // armed, so both gestures coexist and the keyboard path still works.
  if (dragged) cancelConnection('Wire cancelled')
}

export function finishConnection(button) {
  if (!connectionSource) {
    setStatus('Choose an output port first')
    return
  }
  const root = workbench()
  htmx.ajax('POST', `/flows/${root.dataset.flowId}/connections`, {
    target: '#workbench',
    swap: 'outerHTML',
    values: {
      source_id: connectionSource.id,
      source_port: connectionSource.port,
      target_id: button.dataset.inputBlock,
      target_port: button.dataset.inputPort
    }
  })
  cancelConnection('Saving signal connection…')
}

export function drawDraft(event) {
  if (!connectionSource) return
  const draft = document.querySelector('#draft-edge')
  if (!draft) return
  const source = connectionSource.node
  const { blockWidth } = geometry()
  const startX = source.offsetLeft + blockWidth
  const startY = source.offsetTop + Number(connectionSource.button.dataset.portCenter)
  let { x: endX, y: endY } = screenToSheet(event.clientX, event.clientY)
  const targetPort = portUnderPointer(event)
  if (targetIsValid(targetPort)) {
    const target = targetPort.closest('.block-card')
    endX = target.offsetLeft
    endY = target.offsetTop + Number(targetPort.dataset.portCenter)
  }
  draft.setAttribute('d', signalPath(
    { x: startX, y: startY },
    { x: endX, y: endY }
  ))
}
