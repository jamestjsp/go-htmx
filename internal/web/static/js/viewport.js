// =====================================================================
// Viewport: the sheet is a fixed world that the user pans and zooms a
// window across. #sheet carries the transform; the canvas clips it.
//
// Every interaction converts pointer coordinates through screenToSheet.
// Reading offsetLeft/scrollLeft directly, as this file used to, silently
// breaks the moment zoom leaves 100%. screenToSheet lives here rather
// than in geometry.js for that reason: it is the only conversion that
// reads the transform, and geometry.js has to stay free of state so
// every other module can depend on it.
// =====================================================================
import { canvas, sheet, setReadout, setStatus, workbench } from './dom.js'
import { geometry } from './geometry.js'

const MIN_ZOOM = 0.25
const MAX_ZOOM = 4

const viewport = { x: 0, y: 0, zoom: 1 }
const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
let panning = null
let spaceHeld = false
let animateTimer = 0

export const currentZoom = () => viewport.zoom

function viewportKey() {
  const root = workbench()
  return `processlab:viewport:${root ? root.dataset.flowId : 'default'}`
}

export function screenToSheet(clientX, clientY) {
  const root = canvas()
  if (!root) return { x: 0, y: 0 }
  const bounds = root.getBoundingClientRect()
  return {
    x: (clientX - bounds.left - viewport.x) / viewport.zoom,
    y: (clientY - bounds.top - viewport.y) / viewport.zoom
  }
}

export function applyViewport(animate = false) {
  const root = canvas()
  const layer = sheet()
  if (!root || !layer) return
  const { grid } = geometry()
  root.style.setProperty('--pan-x', `${viewport.x}px`)
  root.style.setProperty('--pan-y', `${viewport.y}px`)
  root.style.setProperty('--zoom', String(viewport.zoom))
  root.style.setProperty('--grid-fine', `${grid * viewport.zoom}px`)
  root.style.setProperty('--grid-coarse', `${grid * 4 * viewport.zoom}px`)
  root.dataset.zoomBand = viewport.zoom < 0.6 ? 'coarse' : 'normal'
  if (animate && !reducedMotion.matches) {
    root.dataset.animate = 'true'
    window.clearTimeout(animateTimer)
    animateTimer = window.setTimeout(() => delete root.dataset.animate, 200)
  }
  const readout = document.querySelector('#zoom-readout')
  if (readout) readout.textContent = `${Math.round(viewport.zoom * 100)}%`
  setReadout('#readout-zoom', `${Math.round(viewport.zoom * 100)}%`)
  setReadout('#readout-grid', String(grid))
  saveViewport()
}

export function trackCursorReadout(event) {
  if (!event.target.closest('#flow-canvas')) return
  const point = screenToSheet(event.clientX, event.clientY)
  setReadout('#readout-cursor-x', String(Math.round(point.x)).padStart(4, '0'))
  setReadout('#readout-cursor-y', String(Math.round(point.y)).padStart(4, '0'))
}

function saveViewport() {
  try {
    window.localStorage.setItem(viewportKey(), JSON.stringify(viewport))
  } catch (error) {
    /* storage disabled; the viewport still works, it just will not persist */
  }
}

function loadViewport() {
  try {
    const stored = JSON.parse(window.localStorage.getItem(viewportKey()) || 'null')
    if (!stored || !Number.isFinite(stored.zoom)) return false
    viewport.x = Number(stored.x) || 0
    viewport.y = Number(stored.y) || 0
    viewport.zoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, stored.zoom))
    return true
  } catch (error) {
    return false
  }
}

export function panBy(dx, dy) {
  viewport.x += dx
  viewport.y += dy
  applyViewport()
}

// Zoom about a screen point, keeping whatever sheet coordinate sits
// under that point pinned there.
export function zoomAround(nextZoom, clientX, clientY, animate = false) {
  const root = canvas()
  if (!root) return
  const bounds = root.getBoundingClientRect()
  const px = clientX - bounds.left
  const py = clientY - bounds.top
  const zoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, nextZoom))
  const anchorX = (px - viewport.x) / viewport.zoom
  const anchorY = (py - viewport.y) / viewport.zoom
  viewport.zoom = zoom
  viewport.x = px - anchorX * zoom
  viewport.y = py - anchorY * zoom
  applyViewport(animate)
}

export function zoomByStep(factor) {
  const root = canvas()
  if (!root) return
  const bounds = root.getBoundingClientRect()
  zoomAround(viewport.zoom * factor, bounds.left + bounds.width / 2, bounds.top + bounds.height / 2, true)
}

export function resetZoom() {
  const root = canvas()
  if (!root) return
  const bounds = root.getBoundingClientRect()
  zoomAround(1, bounds.left + bounds.width / 2, bounds.top + bounds.height / 2, true)
  setStatus('Zoom reset to 100%')
}

function contentBounds() {
  const layer = sheet()
  if (!layer) return null
  const blocks = Array.from(layer.querySelectorAll('.block-card'))
  if (!blocks.length) return null
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  blocks.forEach((node) => {
    minX = Math.min(minX, node.offsetLeft)
    minY = Math.min(minY, node.offsetTop)
    maxX = Math.max(maxX, node.offsetLeft + node.offsetWidth)
    maxY = Math.max(maxY, node.offsetTop + node.offsetHeight)
  })
  return { minX, minY, maxX, maxY }
}

export function fitTo(bounds, message) {
  const root = canvas()
  if (!root || !bounds) return
  const box = root.getBoundingClientRect()
  const { blockWidth } = geometry()
  const pad = blockWidth * 0.5
  const width = bounds.maxX - bounds.minX + pad * 2
  const height = bounds.maxY - bounds.minY + pad * 2
  // Never zoom past 100% to fill the window; a two-block sheet blown up
  // to 400% is disorienting, not helpful.
  const zoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM,
    Math.min(1, Math.min(box.width / width, box.height / height))))
  viewport.zoom = zoom
  viewport.x = (box.width - width * zoom) / 2 - (bounds.minX - pad) * zoom
  viewport.y = (box.height - height * zoom) / 2 - (bounds.minY - pad) * zoom
  applyViewport(true)
  if (message) setStatus(message)
}

export function fitView() {
  const bounds = contentBounds()
  if (!bounds) {
    viewport.x = 0
    viewport.y = 0
    viewport.zoom = 1
    applyViewport(true)
    setStatus('Empty sheet; view reset')
    return
  }
  fitTo(bounds, 'Flowsheet fitted to the window')
}

export function beginPan(event) {
  const root = canvas()
  if (!root || !event.target.closest('#flow-canvas')) return false
  const wantsPan = event.button === 1 || (event.button === 0 && spaceHeld)
  if (!wantsPan) return false
  panning = {
    pointerId: event.pointerId,
    startX: event.clientX,
    startY: event.clientY,
    originX: viewport.x,
    originY: viewport.y
  }
  root.setPointerCapture(event.pointerId)
  root.classList.add('is-panning')
  event.preventDefault()
  return true
}

export function movePan(event) {
  if (!panning || event.pointerId !== panning.pointerId) return
  viewport.x = panning.originX + (event.clientX - panning.startX)
  viewport.y = panning.originY + (event.clientY - panning.startY)
  applyViewport()
}

export function endPan(event) {
  if (!panning || event.pointerId !== panning.pointerId) return
  const root = canvas()
  if (root) {
    root.releasePointerCapture(event.pointerId)
    root.classList.remove('is-panning')
  }
  panning = null
}

function setPanCursor() {
  const root = canvas()
  if (root) root.classList.toggle('can-pan', spaceHeld)
}

// Reports whether this press is the one that armed space-to-pan, so a
// key repeat falls through to the rest of the keymap exactly as an
// un-held space would.
export function holdSpace() {
  if (spaceHeld) return false
  spaceHeld = true
  setPanCursor()
  return true
}

export function releaseSpace() {
  spaceHeld = false
  setPanCursor()
}

// A swap can replace the flowsheet itself: clicking a tab swaps a
// different sheet into #workbench. viewportKey() is per flow, so applying
// the outgoing sheet's pan and zoom to the incoming one both misplaces the
// incoming sheet and — because applyViewport() ends in saveViewport() —
// overwrites the incoming sheet's stored view with the outgoing sheet's.
// Whichever event first sees a new flow id therefore loads that sheet's
// own stored viewport, and fits the sheet when it has never been opened.
let openFlowID = workbench() ? workbench().dataset.flowId : ''
let pendingFit = false

function syncViewportToFlow(settled) {
  const root = workbench()
  if (!root) return
  const flowID = root.dataset.flowId || ''
  if (flowID !== openFlowID) {
    openFlowID = flowID
    pendingFit = !loadViewport()
  }
  if (!pendingFit) return
  // fitView() measures the canvas, which is only reliable once the new
  // markup has settled, so the fit runs on both events and is only
  // retired by the settled one.
  fitView()
  if (settled) pendingFit = false
}

// A swap replaces #sheet, so the transform has to be re-stamped or the
// view snaps back to the origin on every edit.
export function reapplyViewport({ settled }) {
  syncViewportToFlow(settled)
  applyViewport()
}

export function initViewport() {
  if (!loadViewport()) {
    applyViewport()
    fitView()
    return
  }
  applyViewport()
}
