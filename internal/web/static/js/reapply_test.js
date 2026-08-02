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
  onBeforeSwap(() => calls.push('before'))
  onReapply(() => calls.push('reapply'))

  assert.equal(listeners.has('htmx:afterSwap'), false)
  listeners.get('htmx:beforeSwap')()
  listeners.get('htmx:afterSettle')()
  listeners.get('htmx:historyRestore')()

  assert.deepEqual(calls, ['before', 'reapply', 'reapply'])
})
