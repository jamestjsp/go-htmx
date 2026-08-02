const DEFAULT_CLEARANCE = 16
const DEFAULT_PORT_STUB = 24
const DEFAULT_BEND_PENALTY = 36
const DEFAULT_CROSSING_PENALTY = 120
const DEFAULT_OVERLAP_PENALTY = 8
const MAX_OBSTACLE_LANES = 12
const MAX_EXHAUSTIVE_VISIBILITY_OBSTACLES = 32
const EPSILON = 1e-6

function pointKey(point) {
  return `${point.x},${point.y}`
}

function samePoint(a, b) {
  return Math.abs(a.x - b.x) < EPSILON && Math.abs(a.y - b.y) < EPSILON
}

function normalizeRect(rect) {
  const left = Number(rect.left ?? rect.x)
  const top = Number(rect.top ?? rect.y)
  const right = Number(rect.right ?? left + Number(rect.width))
  const bottom = Number(rect.bottom ?? top + Number(rect.height))
  return { left, top, right, bottom }
}

export function createObstacleIndex(obstacles, cellSize = 128) {
  const normalized = obstacles.map(normalizeRect)
  const buckets = new Map()
  normalized.forEach((rect, index) => {
    const left = Math.floor(rect.left / cellSize)
    const right = Math.floor(rect.right / cellSize)
    const top = Math.floor(rect.top / cellSize)
    const bottom = Math.floor(rect.bottom / cellSize)
    for (let row = top; row <= bottom; row += 1) {
      for (let column = left; column <= right; column += 1) {
        const key = `${column}:${row}`
        const bucket = buckets.get(key)
        if (bucket) bucket.push(index)
        else buckets.set(key, [index])
      }
    }
  })
  return {
    obstacles: normalized,
    querySegment(a, b, padding = 0) {
      const left = Math.floor((Math.min(a.x, b.x) - padding) / cellSize)
      const right = Math.floor((Math.max(a.x, b.x) + padding) / cellSize)
      const top = Math.floor((Math.min(a.y, b.y) - padding) / cellSize)
      const bottom = Math.floor((Math.max(a.y, b.y) + padding) / cellSize)
      const found = new Set()
      for (let row = top; row <= bottom; row += 1) {
        for (let column = left; column <= right; column += 1) {
          const bucket = buckets.get(`${column}:${row}`) || []
          bucket.forEach((index) => found.add(index))
        }
      }
      return [...found].map((index) => normalized[index])
    }
  }
}

export function createSegmentIndex(cellSize = 128) {
  const segments = []
  const buckets = new Map()
  return {
    addAll(additions) {
      additions.forEach((segment) => {
        const index = segments.length
        segments.push(segment)
        const left = Math.floor(Math.min(segment.a.x, segment.b.x) / cellSize)
        const right = Math.floor(Math.max(segment.a.x, segment.b.x) / cellSize)
        const top = Math.floor(Math.min(segment.a.y, segment.b.y) / cellSize)
        const bottom = Math.floor(Math.max(segment.a.y, segment.b.y) / cellSize)
        for (let row = top; row <= bottom; row += 1) {
          for (let column = left; column <= right; column += 1) {
            const key = `${column}:${row}`
            const bucket = buckets.get(key)
            if (bucket) bucket.push(index)
            else buckets.set(key, [index])
          }
        }
      })
    },
    querySegment(a, b) {
      const left = Math.floor(Math.min(a.x, b.x) / cellSize)
      const right = Math.floor(Math.max(a.x, b.x) / cellSize)
      const top = Math.floor(Math.min(a.y, b.y) / cellSize)
      const bottom = Math.floor(Math.max(a.y, b.y) / cellSize)
      const found = new Set()
      for (let row = top; row <= bottom; row += 1) {
        for (let column = left; column <= right; column += 1) {
          const bucket = buckets.get(`${column}:${row}`) || []
          bucket.forEach((index) => found.add(index))
        }
      }
      return [...found].map((index) => segments[index])
    }
  }
}

function inflateRect(rect, amount) {
  const normalized = normalizeRect(rect)
  return {
    left: normalized.left - amount,
    top: normalized.top - amount,
    right: normalized.right + amount,
    bottom: normalized.bottom + amount
  }
}

function withinBounds(point, bounds) {
  if (!bounds) return true
  return point.x >= bounds.left - EPSILON &&
    point.x <= bounds.right + EPSILON &&
    point.y >= bounds.top - EPSILON &&
    point.y <= bounds.bottom + EPSILON
}

function insideRect(point, rect) {
  return point.x > rect.left + EPSILON &&
    point.x < rect.right - EPSILON &&
    point.y > rect.top + EPSILON &&
    point.y < rect.bottom - EPSILON
}

function segmentCrossesRect(a, b, rect) {
  if (Math.abs(a.y - b.y) < EPSILON) {
    if (a.y <= rect.top + EPSILON || a.y >= rect.bottom - EPSILON) return false
    const low = Math.min(a.x, b.x)
    const high = Math.max(a.x, b.x)
    return high > rect.left + EPSILON && low < rect.right - EPSILON
  }
  if (Math.abs(a.x - b.x) < EPSILON) {
    if (a.x <= rect.left + EPSILON || a.x >= rect.right - EPSILON) return false
    const low = Math.min(a.y, b.y)
    const high = Math.max(a.y, b.y)
    return high > rect.top + EPSILON && low < rect.bottom - EPSILON
  }
  return true
}

function segmentIsClear(a, b, obstacles) {
  return obstacles.every((rect) => !segmentCrossesRect(a, b, rect))
}

function endpointOwner(point, obstacles, output) {
  return obstacles.findIndex((rect) => {
    const edge = output ? rect.right : rect.left
    return Math.abs(point.x - edge) < EPSILON &&
      point.y >= rect.top - EPSILON &&
      point.y <= rect.bottom + EPSILON
  })
}

function stubEnd(point, direction, length, obstacles, owner) {
  let distance = length
  obstacles.forEach((rect, index) => {
    if (index === owner || point.y <= rect.top + EPSILON || point.y >= rect.bottom - EPSILON) return
    if (direction > 0) {
      if (rect.right <= point.x + EPSILON) return
      distance = rect.left <= point.x + EPSILON
        ? 0
        : Math.min(distance, rect.left - point.x)
      return
    }
    if (rect.left >= point.x - EPSILON) return
    distance = rect.right >= point.x - EPSILON
      ? 0
      : Math.min(distance, point.x - rect.right)
  })
  return { x: point.x + direction * Math.max(0, distance), y: point.y }
}

function clearanceLevels(clearance) {
  const levels = []
  let level = Math.max(0, Number(clearance) || 0)
  while (level > EPSILON) {
    levels.push(level)
    if (level <= 1) break
    level /= 2
  }
  levels.push(0)
  return levels
}

function uniqueSorted(values) {
  return [...new Set(values.map(Number))].sort((a, b) => a - b)
}

function direction(a, b) {
  return Math.abs(a.x - b.x) < EPSILON ? 'V' : 'H'
}

function segmentLength(a, b) {
  return Math.abs(a.x - b.x) + Math.abs(a.y - b.y)
}

function overlapLength(a, b, c, d) {
  if (direction(a, b) !== direction(c, d)) return 0
  if (direction(a, b) === 'H') {
    if (Math.abs(a.y - c.y) >= EPSILON) return 0
    return Math.max(0, Math.min(Math.max(a.x, b.x), Math.max(c.x, d.x)) -
      Math.max(Math.min(a.x, b.x), Math.min(c.x, d.x)))
  }
  if (Math.abs(a.x - c.x) >= EPSILON) return 0
  return Math.max(0, Math.min(Math.max(a.y, b.y), Math.max(c.y, d.y)) -
    Math.max(Math.min(a.y, b.y), Math.min(c.y, d.y)))
}

function segmentsCross(a, b, c, d) {
  if (direction(a, b) === direction(c, d)) return false
  const horizontal = direction(a, b) === 'H' ? [a, b] : [c, d]
  const vertical = direction(a, b) === 'V' ? [a, b] : [c, d]
  const x = vertical[0].x
  const y = horizontal[0].y
  return x > Math.min(horizontal[0].x, horizontal[1].x) + EPSILON &&
    x < Math.max(horizontal[0].x, horizontal[1].x) - EPSILON &&
    y > Math.min(vertical[0].y, vertical[1].y) + EPSILON &&
    y < Math.max(vertical[0].y, vertical[1].y) - EPSILON
}

function occupancyCost(a, b, occupied, overlapPenalty, crossingPenalty) {
  let cost = 0
  const candidates = typeof occupied.querySegment === 'function'
    ? occupied.querySegment(a, b)
    : occupied
  for (const segment of candidates) {
    const overlap = overlapLength(a, b, segment.a, segment.b)
    if (overlap > EPSILON) cost += crossingPenalty + overlap * overlapPenalty
    else if (segmentsCross(a, b, segment.a, segment.b)) cost += crossingPenalty
  }
  return cost
}

class MinHeap {
  constructor() {
    this.items = []
    this.sequence = 0
  }

  push(item) {
    item.sequence = this.sequence
    this.sequence += 1
    this.items.push(item)
    let index = this.items.length - 1
    while (index > 0) {
      const parent = Math.floor((index - 1) / 2)
      if (this.compare(this.items[parent], this.items[index]) <= 0) break
      ;[this.items[parent], this.items[index]] = [this.items[index], this.items[parent]]
      index = parent
    }
  }

  pop() {
    if (!this.items.length) return null
    const first = this.items[0]
    const last = this.items.pop()
    if (this.items.length) {
      this.items[0] = last
      let index = 0
      while (true) {
        const left = index * 2 + 1
        const right = left + 1
        let smallest = index
        if (left < this.items.length && this.compare(this.items[left], this.items[smallest]) < 0) {
          smallest = left
        }
        if (right < this.items.length && this.compare(this.items[right], this.items[smallest]) < 0) {
          smallest = right
        }
        if (smallest === index) break
        ;[this.items[index], this.items[smallest]] = [this.items[smallest], this.items[index]]
        index = smallest
      }
    }
    return first
  }

  compare(a, b) {
    return a.priority - b.priority || a.sequence - b.sequence
  }
}

function stateKey(node, incoming) {
  return `${node}:${incoming}`
}

function shortestRoute(graph, occupied, options) {
  if (graph.start === undefined || graph.end === undefined) return null
  const heap = new MinHeap()
  const initial = stateKey(graph.start, '')
  const distance = new Map([[initial, 0]])
  const previous = new Map()
  heap.push({ key: initial, node: graph.start, incoming: '', priority: 0 })

  while (heap.items.length) {
    const current = heap.pop()
    const currentDistance = distance.get(current.key)
    const heuristic = segmentLength(graph.points[current.node], graph.points[graph.end])
    if (current.priority > currentDistance + heuristic + EPSILON) continue
    if (current.node === graph.end) {
      const route = []
      let key = current.key
      while (key) {
        const [node] = key.split(':')
        route.push(graph.points[Number(node)])
        key = previous.get(key)
      }
      return route.reverse()
    }

    const here = graph.points[current.node]
    for (const neighbor of graph.adjacency[current.node]) {
      const there = graph.points[neighbor]
      const incoming = direction(here, there)
      const bend = current.incoming && current.incoming !== incoming ? options.bendPenalty : 0
      const nextDistance = currentDistance + segmentLength(here, there) + bend + occupancyCost(
        here,
        there,
        occupied,
        options.overlapPenalty,
        options.crossingPenalty
      )
      const key = stateKey(neighbor, incoming)
      if (nextDistance >= (distance.get(key) ?? Infinity) - EPSILON) continue
      distance.set(key, nextDistance)
      previous.set(key, current.key)
      heap.push({
        key,
        node: neighbor,
        incoming,
        priority: nextDistance + segmentLength(there, graph.points[graph.end])
      })
    }
  }
  return null
}

export function simplifyRoute(points) {
  const simplified = []
  for (const point of points) {
    if (simplified.length && samePoint(simplified[simplified.length - 1], point)) continue
    simplified.push(point)
    while (simplified.length >= 3) {
      const a = simplified[simplified.length - 3]
      const b = simplified[simplified.length - 2]
      const c = simplified[simplified.length - 1]
      if (direction(a, b) !== direction(b, c)) break
      simplified.splice(simplified.length - 2, 1)
    }
  }
  return simplified
}

function boundedObstacleLanes(values, anchors, bounds) {
  const unique = uniqueSorted(values).filter((value) => {
    return !bounds || value >= bounds[0] - EPSILON && value <= bounds[1] + EPSILON
  })
  if (unique.length <= MAX_OBSTACLE_LANES) return unique
  const ranked = [...unique].sort((a, b) => {
    const distanceA = Math.min(...anchors.map((anchor) => Math.abs(a - anchor)))
    const distanceB = Math.min(...anchors.map((anchor) => Math.abs(b - anchor)))
    return distanceA - distanceB || a - b
  })
  return uniqueSorted([
    unique[0],
    unique.at(-1),
    ...ranked.slice(0, MAX_OBSTACLE_LANES - 2)
  ])
}

function visibilityObstacleLanes(values, anchors, bounds, obstacleCount) {
  if (obstacleCount > MAX_EXHAUSTIVE_VISIBILITY_OBSTACLES) {
    return boundedObstacleLanes(values, anchors, bounds)
  }
  return uniqueSorted(values).filter((value) => {
    return !bounds || value >= bounds[0] - EPSILON && value <= bounds[1] + EPSILON
  })
}

function addNeighbor(adjacency, from, to) {
  if (from === to || adjacency[from].includes(to)) return
  adjacency[from].push(to)
  adjacency[to].push(from)
}

function buildBoundedVisibilityGraph(start, end, obstacles, bounds, isClear) {
  const xs = uniqueSorted([
    start.x,
    end.x,
    ...(bounds ? [bounds.left, bounds.right] : []),
    ...visibilityObstacleLanes(
      obstacles.flatMap((rect) => [rect.left, rect.right]),
      [start.x, end.x],
      bounds && [bounds.left, bounds.right],
      obstacles.length
    )
  ])
  const ys = uniqueSorted([
    start.y,
    end.y,
    ...(bounds ? [bounds.top, bounds.bottom] : []),
    ...visibilityObstacleLanes(
      obstacles.flatMap((rect) => [rect.top, rect.bottom]),
      [start.y, end.y],
      bounds && [bounds.top, bounds.bottom],
      obstacles.length
    )
  ])
  const points = []
  const indexByPoint = new Map()
  const rows = new Map()
  const columns = new Map()

  for (const y of ys) {
    for (const x of xs) {
      const point = { x, y }
      if (!withinBounds(point, bounds) || obstacles.some((rect) => insideRect(point, rect))) {
        continue
      }
      const index = points.length
      points.push(point)
      indexByPoint.set(pointKey(point), index)
      if (!rows.has(y)) rows.set(y, [])
      if (!columns.has(x)) columns.set(x, [])
      rows.get(y).push(index)
      columns.get(x).push(index)
    }
  }

  const adjacency = points.map(() => [])
  for (const row of rows.values()) {
    row.sort((a, b) => points[a].x - points[b].x)
    for (let index = 1; index < row.length; index += 1) {
      const from = row[index - 1]
      const to = row[index]
      if (isClear(points[from], points[to])) addNeighbor(adjacency, from, to)
    }
  }
  for (const column of columns.values()) {
    column.sort((a, b) => points[a].y - points[b].y)
    for (let index = 1; index < column.length; index += 1) {
      const from = column[index - 1]
      const to = column[index]
      if (isClear(points[from], points[to])) addNeighbor(adjacency, from, to)
    }
  }

  return {
    points,
    adjacency,
    start: indexByPoint.get(pointKey(start)),
    end: indexByPoint.get(pointKey(end))
  }
}

function manhattanCandidates(sourceExit, targetEntry, obstacles, bounds, includeObstacleLanes) {
  const anchorXs = [
    sourceExit.x,
    targetEntry.x,
    (sourceExit.x + targetEntry.x) / 2
  ]
  const anchorYs = [
    sourceExit.y,
    targetEntry.y,
    (sourceExit.y + targetEntry.y) / 2
  ]
  const obstacleXs = includeObstacleLanes
    ? boundedObstacleLanes(
        obstacles.flatMap((rect) => [rect.left, rect.right]),
        anchorXs,
        bounds && [bounds.left, bounds.right]
      )
    : []
  const obstacleYs = includeObstacleLanes
    ? boundedObstacleLanes(
        obstacles.flatMap((rect) => [rect.top, rect.bottom]),
        anchorYs,
        bounds && [bounds.top, bounds.bottom]
      )
    : []
  const candidateXs = uniqueSorted([...anchorXs, ...obstacleXs]).filter((x) => {
    if (sourceExit.x <= targetEntry.x) {
      return x >= sourceExit.x - EPSILON && x <= targetEntry.x + EPSILON
    }
    return x >= sourceExit.x - EPSILON
  })
  const candidateYs = uniqueSorted([...anchorYs, ...obstacleYs]).filter((y) => {
    return sourceExit.x <= targetEntry.x || Math.abs(y - sourceExit.y) >= EPSILON
  })
  const candidates = [
    ...candidateXs.map((x) => [
      sourceExit,
      { x, y: sourceExit.y },
      { x, y: targetEntry.y },
      targetEntry
    ]),
    ...candidateYs.map((y) => [
      sourceExit,
      { x: sourceExit.x, y },
      { x: targetEntry.x, y },
      targetEntry
    ])
  ]
  const seen = new Set()
  return candidates.filter((points) => {
    const key = routePath(simplifyRoute(points))
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

function candidateCost(points, obstacles, occupied, options, penalizeCrossings) {
  if (points.some((point) => !withinBounds(point, options.bounds))) return Infinity
  const segments = routeSegments(points)
  let cost = 0
  let incoming = ''
  for (const segment of segments) {
    const crossingCount = penalizeCrossings
      ? obstacles.reduce((count, rect) => {
          return count + (segmentCrossesRect(segment.a, segment.b, rect) ? 1 : 0)
        }, 0)
      : 0
    const nextDirection = direction(segment.a, segment.b)
    if (incoming && incoming !== nextDirection) cost += options.bendPenalty
    incoming = nextDirection
    cost += segmentLength(segment.a, segment.b)
    cost += crossingCount * 1_000_000
    cost += occupancyCost(
      segment.a,
      segment.b,
      occupied,
      options.overlapPenalty,
      options.crossingPenalty
    )
  }
  return cost
}

function chooseCandidate(
  source,
  target,
  sourceExit,
  targetEntry,
  obstacles,
  occupied,
  bounds,
  options,
  requireClear,
  isClear = (a, b) => segmentIsClear(a, b, obstacles)
) {
  const rank = (candidates, penalizeCrossings) => {
    const ranked = []
    candidates.forEach((main, order) => {
      if (requireClear && !routeSegments(main).every(({ a, b }) => isClear(a, b))) return
      const points = simplifyRoute([source, ...main, target])
      const baseCost = candidateCost(
        points,
        obstacles,
        [],
        { ...options, bounds },
        penalizeCrossings
      )
      if (Number.isFinite(baseCost)) ranked.push({ points, baseCost, order })
    })
    ranked.sort((a, b) => a.baseCost - b.baseCost || a.order - b.order)
    if (!ranked.length) return null
    const primary = ranked[0]
    const primaryCost = candidateCost(
      primary.points,
      obstacles,
      occupied,
      { ...options, bounds },
      penalizeCrossings
    )
    if (primaryCost <= primary.baseCost + EPSILON) return primary.points
    let best = { ...primary, cost: primaryCost }
    ranked.slice(1).forEach((candidate) => {
      const cost = candidateCost(
        candidate.points,
        obstacles,
        occupied,
        { ...options, bounds },
        penalizeCrossings
      )
      if (cost < best.cost - EPSILON ||
          Math.abs(cost - best.cost) < EPSILON && candidate.order < best.order) {
        best = { ...candidate, cost }
      }
    })
    return best.points
  }
  if (!requireClear) {
    return rank(
      manhattanCandidates(sourceExit, targetEntry, obstacles, bounds, true),
      true
    )
  }
  const direct = rank(
    manhattanCandidates(sourceExit, targetEntry, obstacles, bounds, false),
    false
  )
  if (direct) return direct
  const obstacleLane = rank(
    manhattanCandidates(sourceExit, targetEntry, obstacles, bounds, true),
    false
  )
  if (obstacleLane) return obstacleLane
  const graph = buildBoundedVisibilityGraph(
    sourceExit,
    targetEntry,
    obstacles,
    bounds,
    isClear
  )
  const main = shortestRoute(graph, occupied, options)
  return main ? simplifyRoute([source, ...main, target]) : null
}

function fallbackRoute(start, end, obstacles, bounds, options) {
  const sourceOwner = endpointOwner(start, obstacles, true)
  const targetOwner = endpointOwner(end, obstacles, false)
  const sourceExit = stubEnd(start, 1, options.portStub, obstacles, sourceOwner)
  const targetEntry = stubEnd(end, -1, options.portStub, obstacles, targetOwner)
  return chooseCandidate(
    start,
    end,
    sourceExit,
    targetEntry,
    obstacles,
    [],
    bounds,
    options,
    false
  )
}

function routeWithClearance(
  source,
  target,
  obstacles,
  occupied,
  bounds,
  options,
  clearance,
  obstacleIndex
) {
  const expanded = obstacles.map((rect) => inflateRect(rect, clearance))
  const sourceOwner = endpointOwner(source, obstacles, true)
  const targetOwner = endpointOwner(target, obstacles, false)
  const sourceExit = stubEnd(source, 1, options.portStub, expanded, sourceOwner)
  const targetEntry = stubEnd(target, -1, options.portStub, expanded, targetOwner)
  if (sourceOwner >= 0 && sourceExit.x - source.x < clearance - EPSILON) return null
  if (targetOwner >= 0 && target.x - targetEntry.x < clearance - EPSILON) return null
  const isClear = obstacleIndex
    ? (a, b) => obstacleIndex.querySegment(a, b, clearance)
        .every((rect) => !segmentCrossesRect(a, b, inflateRect(rect, clearance)))
    : (a, b) => segmentIsClear(a, b, expanded)
  return chooseCandidate(
    source,
    target,
    sourceExit,
    targetEntry,
    expanded,
    occupied,
    bounds,
    options,
    true,
    isClear
  )
}

export function routeOrthogonal({
  start,
  end,
  obstacles = [],
  occupied = [],
  bounds = null,
  clearance = DEFAULT_CLEARANCE,
  portStub = DEFAULT_PORT_STUB,
  bendPenalty = DEFAULT_BEND_PENALTY,
  crossingPenalty = DEFAULT_CROSSING_PENALTY,
  overlapPenalty = DEFAULT_OVERLAP_PENALTY,
  obstacleIndex = null
}) {
  const source = { x: Number(start.x), y: Number(start.y) }
  const target = { x: Number(end.x), y: Number(end.y) }
  const normalized = obstacleIndex?.obstacles || obstacles.map(normalizeRect)
  const options = {
    portStub,
    bendPenalty,
    crossingPenalty,
    overlapPenalty
  }
  for (const level of clearanceLevels(clearance)) {
    const route = routeWithClearance(
      source,
      target,
      normalized,
      occupied,
      bounds,
      options,
      level,
      obstacleIndex
    )
    if (route) return route
  }
  return fallbackRoute(source, target, normalized, bounds, options)
}

export function routeSegments(points) {
  const segments = []
  for (let index = 1; index < points.length; index += 1) {
    segments.push({ a: points[index - 1], b: points[index] })
  }
  return segments
}

export function routeIntersectsRect(segments, rect, clearance = 0) {
  const obstacle = inflateRect(rect, clearance)
  return segments.some((segment) => segmentCrossesRect(segment.a, segment.b, obstacle))
}

export function routePath(points) {
  if (!points.length) return ''
  return points.reduce((path, point, index) => {
    const command = index === 0 ? 'M' : 'L'
    return `${path}${index === 0 ? '' : ' '}${command} ${point.x} ${point.y}`
  }, '')
}
