// =====================================================================
// Sheet geometry. No state of its own: every value is read back off the
// server-rendered canvas, so this is the one module the rest can depend
// on without inheriting a dependency on anything mutable.
// =====================================================================
import { canvas } from './dom.js'
import { routeOrthogonal, routePath, routeSegments } from './orthogonal-routing.js'

const DEFAULT_GEOMETRY = { width: 6000, height: 4000, grid: 20, blockWidth: 172, blockHeight: 84 }

// The server owns the sheet constants; the client reads them off the
// canvas so the grid, the snap step, and the bounds cannot drift apart.
export function geometry() {
  const root = canvas()
  if (!root) return DEFAULT_GEOMETRY
  const read = (name, fallback) => Number(root.dataset[name]) || fallback
  return {
    width: read('sheetWidth', DEFAULT_GEOMETRY.width),
    height: read('sheetHeight', DEFAULT_GEOMETRY.height),
    grid: read('sheetGrid', DEFAULT_GEOMETRY.grid),
    blockWidth: read('blockWidth', DEFAULT_GEOMETRY.blockWidth),
    blockHeight: read('blockHeight', DEFAULT_GEOMETRY.blockHeight)
  }
}

function routingContext(root) {
  const { width, height, blockWidth, blockHeight } = geometry()
  const obstacles = [...root.querySelectorAll('.block-card')].map((block) => ({
    left: block.offsetLeft,
    top: block.offsetTop,
    right: block.offsetLeft + blockWidth,
    bottom: block.offsetTop + blockHeight
  }))
  return {
    obstacles,
    bounds: { left: 0, top: 0, right: width, bottom: height }
  }
}

function endpoint(block, center, output) {
  const { blockWidth, blockHeight } = geometry()
  const offset = Number(center) || blockHeight / 2
  return {
    x: block.offsetLeft + (output ? blockWidth : 0),
    y: block.offsetTop + offset
  }
}

function computeRoute(start, end, occupied, context) {
  const points = routeOrthogonal({
    start,
    end,
    occupied,
    obstacles: context.obstacles,
    bounds: context.bounds
  })
  return { path: routePath(points), segments: routeSegments(points) }
}

export function signalPath(start, end) {
  const root = canvas()
  if (!root) return ''
  return computeRoute(start, end, [], routingContext(root)).path
}

function setConnectionPath(root, edge, path) {
  root.querySelectorAll(`[data-edge-id="${edge.dataset.edgeId}"]`).forEach((element) => {
    element.setAttribute('d', path)
  })
}

export function redrawEdges() {
  const root = canvas()
  if (!root) return
  const context = routingContext(root)
  const occupied = []
  root.querySelectorAll('.signal-line[data-edge-source]').forEach((edge) => {
    const source = root.querySelector(`[data-block-id="${edge.dataset.edgeSource}"]`)
    const target = root.querySelector(`[data-block-id="${edge.dataset.edgeTarget}"]`)
    if (!source || !target) return
    const start = endpoint(source, edge.dataset.edgeSourceCenter, true)
    const end = endpoint(target, edge.dataset.edgeTargetCenter, false)
    const route = computeRoute(start, end, occupied, context)
    setConnectionPath(root, edge, route.path)
    occupied.push(...route.segments)
  })
}
