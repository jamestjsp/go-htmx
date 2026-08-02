// =====================================================================
// Sheet geometry and derived route state. Persisted values are always
// read from the server-rendered canvas; the cache only avoids rebuilding
// unaffected paths during one live drag.
// =====================================================================
import { canvas } from './dom.js'
import {
  routeIntersectsRect,
  routeOrthogonal,
  routePath,
  routeSegments
} from './orthogonal-routing.js'

const DEFAULT_GEOMETRY = { width: 6000, height: 4000, grid: 20, blockWidth: 172, blockHeight: 84 }
const ROUTE_CLEARANCE = 16
const routeCache = new Map()

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
  const obstaclesByBlock = new Map()
  const obstacles = [...root.querySelectorAll('.block-card')].map((block) => {
    const obstacle = {
      left: block.offsetLeft,
      top: block.offsetTop,
      right: block.offsetLeft + blockWidth,
      bottom: block.offsetTop + blockHeight
    }
    obstaclesByBlock.set(block.dataset.blockId, obstacle)
    return obstacle
  })
  return {
    obstacles,
    obstaclesByBlock,
    bounds: { left: 0, top: 0, right: width, bottom: height },
    blockWidth,
    blockHeight
  }
}

function endpoint(block, center, output, context) {
  const offset = Number(center) || context.blockHeight / 2
  return {
    x: block.offsetLeft + (output ? context.blockWidth : 0),
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

export function routeAffectedByBlocks(route, movedBlockIDs, movedRects) {
  if (!route?.segments) return true
  if (movedBlockIDs.has(String(route.sourceID)) || movedBlockIDs.has(String(route.targetID))) return true
  return movedRects.some((rect) => routeIntersectsRect(route.segments, rect, ROUTE_CLEARANCE))
}

function setConnectionPath(root, edge, path) {
  root.querySelectorAll(`[data-edge-id="${edge.dataset.edgeId}"]`).forEach((element) => {
    element.setAttribute('d', path)
  })
}

export function redrawEdges(blockIDs = null) {
  const root = canvas()
  if (!root) return
  const context = routingContext(root)
  const movedBlockIDs = Array.isArray(blockIDs) ? new Set(blockIDs.map(String)) : null
  const movedRects = movedBlockIDs === null
    ? []
    : [...movedBlockIDs].map((id) => context.obstaclesByBlock.get(id)).filter(Boolean)
  const occupied = []
  const liveEdges = new Set()
  root.querySelectorAll('.signal-line[data-edge-source]').forEach((edge) => {
    const edgeID = edge.dataset.edgeId
    liveEdges.add(edgeID)
    const source = root.querySelector(`[data-block-id="${edge.dataset.edgeSource}"]`)
    const target = root.querySelector(`[data-block-id="${edge.dataset.edgeTarget}"]`)
    if (!source || !target) return
    const cached = routeCache.get(edgeID)
    const affected = movedBlockIDs === null ||
      routeAffectedByBlocks(cached, movedBlockIDs, movedRects)
    let route = cached
    if (affected) {
      const start = endpoint(source, edge.dataset.edgeSourceCenter, true, context)
      const end = endpoint(target, edge.dataset.edgeTargetCenter, false, context)
      route = {
        ...computeRoute(start, end, occupied, context),
        sourceID: edge.dataset.edgeSource,
        targetID: edge.dataset.edgeTarget
      }
      routeCache.set(edgeID, route)
      setConnectionPath(root, edge, route.path)
    }
    occupied.push(...route.segments)
  })
  if (movedBlockIDs === null) {
    for (const edgeID of routeCache.keys()) {
      if (!liveEdges.has(edgeID)) routeCache.delete(edgeID)
    }
  }
}

export function createRedrawScheduler(redraw, requestFrame, cancelFrame) {
  let frame = null
  let token = 0
  let full = false
  const blockIDs = new Set()

  const deliver = () => {
    const selection = full ? null : [...blockIDs]
    full = false
    blockIDs.clear()
    redraw(selection)
  }
  const schedule = (changedBlockIDs = null) => {
    if (changedBlockIDs === null) full = true
    else changedBlockIDs.forEach((id) => blockIDs.add(String(id)))
    if (frame !== null) return
    const scheduledToken = ++token
    frame = requestFrame(() => {
      if (scheduledToken !== token) return
      frame = null
      deliver()
    })
  }
  const flush = (forceFull = false) => {
    if (forceFull) full = true
    if (frame !== null) {
      cancelFrame(frame)
      frame = null
      token += 1
    }
    if (full || blockIDs.size) deliver()
  }
  return { schedule, flush }
}

const redrawScheduler = createRedrawScheduler(
  redrawEdges,
  (callback) => window.requestAnimationFrame(callback),
  (frame) => window.cancelAnimationFrame(frame)
)

export const scheduleRedrawEdges = (blockIDs) => redrawScheduler.schedule(blockIDs)
export const flushRedrawEdges = () => redrawScheduler.flush(true)
