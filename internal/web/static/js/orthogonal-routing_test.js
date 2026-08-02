import assert from 'node:assert/strict'
import test from 'node:test'

import {
  routeOrthogonal,
  routePath,
  routeSegments
} from './orthogonal-routing.js'

const block = (left, top, width = 172, height = 84) => ({
  left,
  top,
  right: left + width,
  bottom: top + height
})

function assertOrthogonal(points) {
  assert.ok(points.length >= 2)
  for (const { a, b } of routeSegments(points)) {
    assert.ok(a.x === b.x || a.y === b.y, `diagonal segment ${JSON.stringify({ a, b })}`)
  }
}

function segmentEntersRect(a, b, rect) {
  if (a.y === b.y) {
    return a.y > rect.top && a.y < rect.bottom &&
      Math.max(a.x, b.x) > rect.left && Math.min(a.x, b.x) < rect.right
  }
  return a.x > rect.left && a.x < rect.right &&
    Math.max(a.y, b.y) > rect.top && Math.min(a.y, b.y) < rect.bottom
}

test('uses the direct orthogonal channel for unobstructed forward flow', () => {
  const points = routeOrthogonal({
    start: { x: 272, y: 242 },
    end: { x: 400, y: 542 },
    obstacles: [block(100, 200), block(400, 500)],
    bounds: { left: 0, top: 0, right: 1200, bottom: 900 }
  })

  assertOrthogonal(points)
  assert.deepEqual(points, [
    { x: 272, y: 242 },
    { x: 296, y: 242 },
    { x: 296, y: 542 },
    { x: 400, y: 542 }
  ])
})

test('routes around an intervening block with clearance', () => {
  const obstacle = block(360, 180)
  const points = routeOrthogonal({
    start: { x: 272, y: 222 },
    end: { x: 620, y: 222 },
    obstacles: [block(100, 180), obstacle, block(620, 180)],
    bounds: { left: 0, top: 0, right: 1200, bottom: 900 }
  })

  assertOrthogonal(points)
  const inflated = {
    left: obstacle.left - 16,
    top: obstacle.top - 16,
    right: obstacle.right + 16,
    bottom: obstacle.bottom + 16
  }
  for (const { a, b } of routeSegments(points)) {
    assert.equal(segmentEntersRect(a, b, inflated), false, JSON.stringify({ a, b }))
  }
})

test('routes right-to-left feedback outside both endpoint blocks', () => {
  const source = block(800, 200)
  const target = block(320, 420)
  const points = routeOrthogonal({
    start: { x: source.right, y: 242 },
    end: { x: target.left, y: 462 },
    obstacles: [source, target, block(560, 200)],
    bounds: { left: 0, top: 0, right: 1400, bottom: 900 }
  })

  assertOrthogonal(points)
  assert.deepEqual(points[1], { x: source.right + 24, y: 242 })
  assert.deepEqual(points.at(-2), { x: target.left - 24, y: 462 })
  for (const rect of [source, target]) {
    for (const { a, b } of routeSegments(points).slice(1, -1)) {
      const inflated = {
        left: rect.left - 16,
        top: rect.top - 16,
        right: rect.right + 16,
        bottom: rect.bottom + 16
      }
      assert.equal(segmentEntersRect(a, b, inflated), false, JSON.stringify({ a, b, inflated }))
    }
  }
})

test('uses occupancy cost to avoid an available shared segment', () => {
  const request = {
    start: { x: 272, y: 242 },
    end: { x: 600, y: 442 },
    obstacles: [block(100, 200), block(600, 400)],
    bounds: { left: 0, top: 0, right: 1200, bottom: 900 }
  }
  const first = routeOrthogonal(request)
  const second = routeOrthogonal({ ...request, occupied: routeSegments(first) })

  assertOrthogonal(second)
  assert.notEqual(routePath(second), routePath(first))
})

test('is deterministic and retains an orthogonal fallback for blocked geometry', () => {
  const request = {
    start: { x: 200, y: 200 },
    end: { x: 180, y: 240 },
    obstacles: [block(0, 0, 400, 400)],
    bounds: { left: 0, top: 0, right: 400, bottom: 400 }
  }
  const first = routeOrthogonal(request)
  const second = routeOrthogonal(request)

  assertOrthogonal(first)
  assert.deepEqual(second, first)
  assert.match(routePath(first), /^M 200 200 L /)
})

test('shortens a blocked input stub instead of crossing an adjacent block', () => {
  const adjacent = block(243, 200)
  const target = block(426, 200)
  const source = block(700, 380)
  const points = routeOrthogonal({
    start: { x: source.right, y: 422 },
    end: { x: target.left, y: 242 },
    obstacles: [adjacent, target, source],
    bounds: { left: 0, top: 0, right: 1200, bottom: 900 }
  })

  assertOrthogonal(points)
  assert.ok(points.at(-2).x >= adjacent.right)
  assert.ok(points.at(-2).x < target.left)
  for (const { a, b } of routeSegments(points)) {
    assert.equal(segmentEntersRect(a, b, adjacent), false, JSON.stringify({ a, b, adjacent }))
  }
})
