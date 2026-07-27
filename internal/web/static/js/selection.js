// =====================================================================
// Selection.
//
// Multi-selection is client state on purpose: the server keeps its
// single `selected` parameter for the inspector, so the HTMX contract is
// untouched and a marquee drag costs no round trips. A swap replaces
// every block element, so the set is re-applied afterwards and ids that
// no longer exist are dropped.
//
// The set itself never leaves this module. Everything else asks through
// isSelected(), selectionSize(), and selectedNodes(), so a module that
// wants to know what is selected cannot accidentally change it.
// =====================================================================
import { canvas, setStatus, sheet, workbench } from './dom.js'
import { geometry } from './geometry.js'
import { currentZoom, fitTo, screenToSheet } from './viewport.js'

const selection = new Set()
let marquee = null

export const selectionSize = () => selection.size
export const isSelected = (id) => selection.has(id)

export function blockNodes() {
  const layer = sheet()
  return layer ? Array.from(layer.querySelectorAll('.block-card')) : []
}

export function selectedNodes() {
  return blockNodes().filter((node) => selection.has(node.dataset.blockId))
}

export function applySelection() {
  const nodes = blockNodes()
  if (!nodes.length) return
  const present = new Set(nodes.map((node) => node.dataset.blockId))
  selection.forEach((id) => {
    if (!present.has(id)) selection.delete(id)
  })
  // With nothing selected, defer to whatever the server marked, so a
  // swap does not silently drop the inspector's highlight.
  if (!selection.size) {
    const root = workbench()
    const serverSelected = root && root.dataset.selectedId
    if (serverSelected) selection.add(serverSelected)
  }
  nodes.forEach((node) => {
    node.classList.toggle('selected', selection.has(node.dataset.blockId))
  })
  renderSelectionBar()
  updateSelectionReadout()
}

export function setSelection(ids) {
  selection.clear()
  ids.forEach((id) => selection.add(id))
  applySelection()
}

export function toggleSelection(id) {
  if (selection.has(id)) selection.delete(id)
  else selection.add(id)
  applySelection()
  setStatus(`${selection.size} block${selection.size === 1 ? '' : 's'} selected`)
}

function updateSelectionReadout() {
  const node = document.querySelector('#readout-selection')
  if (!node) return
  node.textContent = String(selection.size)
  node.dataset.count = String(selection.size)
}

function selectionBounds() {
  const nodes = selectedNodes()
  if (!nodes.length) return null
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  nodes.forEach((node) => {
    minX = Math.min(minX, node.offsetLeft)
    minY = Math.min(minY, node.offsetTop)
    maxX = Math.max(maxX, node.offsetLeft + node.offsetWidth)
    maxY = Math.max(maxY, node.offsetTop + node.offsetHeight)
  })
  return { minX, minY, maxX, maxY }
}

function renderSelectionBar() {
  const root = canvas()
  if (!root) return
  let bar = root.querySelector('[data-selection-bar]')
  if (selection.size < 2) {
    if (bar) bar.remove()
    return
  }
  if (!bar) {
    bar = document.createElement('div')
    bar.dataset.selectionBar = ''
    bar.style.cssText = [
      'position:absolute', 'left:50%', 'bottom:14px', 'transform:translateX(-50%)',
      'z-index:25', 'display:flex', 'align-items:center', 'gap:10px',
      'padding:7px 9px 7px 13px', 'border-radius:8px',
      'background:var(--housing,#16201e)', 'color:var(--ink-inverse,#e8efec)',
      'font-size:12px', 'box-shadow:0 10px 26px rgb(10 20 18 / 34%)'
    ].join(';')
    bar.innerHTML =
      '<span data-selection-count></span>' +
      '<button type="button" data-selection-fit>Fit</button>' +
      '<button type="button" data-selection-delete>Delete</button>'
    bar.querySelectorAll('button').forEach((button) => {
      button.style.cssText = [
        'padding:5px 10px', 'border:1px solid var(--housing-line-strong,#3c4f4a)',
        'border-radius:5px', 'background:var(--housing-raised,#1f2c29)',
        'color:inherit', 'cursor:pointer', 'font-size:11px', 'font-weight:650'
      ].join(';')
    })
    root.appendChild(bar)
  }
  bar.querySelector('[data-selection-count]').textContent = `${selection.size} blocks selected`
}

export function fitSelection() {
  const bounds = selectionBounds()
  if (bounds) fitTo(bounds, `Fitted to ${selection.size} selected blocks`)
}

export function selectAll() {
  const ids = blockNodes().map((node) => node.dataset.blockId)
  setSelection(ids)
  setStatus(`${ids.length} blocks selected`)
}

export function deleteSelection() {
  const root = workbench()
  if (!root || !selection.size) return
  const ids = Array.from(selection)
  if (ids.length > 1 && !window.confirm(`Delete ${ids.length} blocks and their signal wires?`)) return
  const query = ids.map((id) => `id=${encodeURIComponent(id)}`).join('&')
  htmx.ajax('DELETE', `/flows/${root.dataset.flowId}/blocks?${query}`, {
    target: '#workbench',
    swap: 'outerHTML'
  })
  selection.clear()
  setStatus(`Deleted ${ids.length} block${ids.length === 1 ? '' : 's'}`)
}

export function duplicateSelection() {
  const root = workbench()
  if (!root || !selection.size) return
  htmx.ajax('POST', `/flows/${root.dataset.flowId}/blocks/duplicate`, {
    target: '#workbench',
    swap: 'outerHTML',
    values: { id: Array.from(selection) }
  })
  setStatus(`Duplicated ${selection.size} block${selection.size === 1 ? '' : 's'}`)
}

export function selectBlock(node) {
  const root = workbench()
  if (!root || !node) return
  setSelection([node.dataset.blockId])
  htmx.ajax('GET', `/flows/${root.dataset.flowId}/workbench?selected=${node.dataset.blockId}`, {
    target: '#workbench',
    swap: 'outerHTML'
  })
}

// ---- marquee --------------------------------------------------------

function marqueeRect(start, current) {
  return {
    minX: Math.min(start.x, current.x),
    minY: Math.min(start.y, current.y),
    maxX: Math.max(start.x, current.x),
    maxY: Math.max(start.y, current.y)
  }
}

function blocksWithin(rect) {
  const { blockWidth, blockHeight } = geometry()
  return blockNodes().filter((node) => {
    const left = node.offsetLeft
    const top = node.offsetTop
    return left < rect.maxX && left + blockWidth > rect.minX &&
      top < rect.maxY && top + blockHeight > rect.minY
  })
}

export function beginMarquee(event) {
  const root = canvas()
  if (!root || event.button !== 0) return false
  if (!event.target.closest('#flow-canvas')) return false
  if (event.target.closest('.block-card, .port, [data-selection-bar], .canvas-legend')) return false
  marquee = {
    pointerId: event.pointerId,
    start: screenToSheet(event.clientX, event.clientY),
    base: event.shiftKey ? Array.from(selection) : [],
    moved: false,
    element: null,
    current: null
  }
  if (!event.shiftKey) setSelection([])
  root.setPointerCapture(event.pointerId)
  event.preventDefault()
  return true
}

export function moveMarquee(event) {
  if (!marquee || event.pointerId !== marquee.pointerId) return
  const layer = sheet()
  if (!layer) return
  marquee.current = screenToSheet(event.clientX, event.clientY)
  marquee.moved = true
  const rect = marqueeRect(marquee.start, marquee.current)
  if (!marquee.element || marquee.element.parentNode !== layer) {
    marquee.element = document.createElement('div')
    marquee.element.dataset.marquee = ''
    layer.appendChild(marquee.element)
  }
  const hairline = 1 / currentZoom()
  marquee.element.style.cssText = [
    'position:absolute', `left:${rect.minX}px`, `top:${rect.minY}px`,
    `width:${rect.maxX - rect.minX}px`, `height:${rect.maxY - rect.minY}px`,
    `border:${hairline}px solid var(--probe-deep,#0d6156)`,
    'background:var(--probe-glow,rgb(53 179 156 / 20%))',
    'pointer-events:none', 'z-index:19'
  ].join(';')
  // Live feedback: highlight as the band sweeps, not only on release.
  setSelection(marquee.base.concat(blocksWithin(rect).map((node) => node.dataset.blockId)))
}

export function endMarquee(event) {
  if (!marquee || event.pointerId !== marquee.pointerId) return
  const root = canvas()
  if (root) root.releasePointerCapture(event.pointerId)
  if (marquee.element) marquee.element.remove()
  const { moved, start, current, base } = marquee
  marquee = null
  if (!moved || !current) {
    setSelection(base)
    return
  }
  const ids = blocksWithin(marqueeRect(start, current)).map((node) => node.dataset.blockId)
  setSelection(base.concat(ids))
  setStatus(selection.size
    ? `${selection.size} block${selection.size === 1 ? '' : 's'} selected`
    : 'Selection cleared')
}
