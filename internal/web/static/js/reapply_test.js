import assert from 'node:assert/strict'
import test from 'node:test'

const listeners = new Map()
globalThis.document = {
  addEventListener(name, listener) {
    listeners.set(name, listener)
  }
}

const { onBeforeSwap, onReapply } = await import('./reapply.js')

test('rebuilds client state only after settle and on history restore', () => {
  const calls = []
  onBeforeSwap((event) => calls.push(`before:${event.type}`))
  onReapply((event) => calls.push(`reapply:${event.type}`))

  assert.equal(listeners.has('htmx:afterSwap'), false)
  listeners.get('htmx:beforeSwap')({ type: 'htmx:beforeSwap' })
  listeners.get('htmx:afterSettle')({ type: 'htmx:afterSettle' })
  listeners.get('htmx:historyRestore')({ type: 'htmx:historyRestore' })

  assert.deepEqual(calls, [
    'before:htmx:beforeSwap',
    'reapply:htmx:afterSettle',
    'reapply:htmx:historyRestore'
  ])
})
