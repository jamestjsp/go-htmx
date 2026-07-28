import { workbench } from './dom.js'

const storageKey = () => {
  const root = workbench()
  return `processlab:hidden-series:${root ? root.dataset.flowId : 'default'}`
}

function hiddenSeries() {
  try {
    const stored = JSON.parse(localStorage.getItem(storageKey()) || '[]')
    return new Set(Array.isArray(stored) ? stored.map(String) : [])
  } catch {
    return new Set()
  }
}

function saveHidden(series) {
  localStorage.setItem(storageKey(), JSON.stringify([...series].sort()))
}

export function applySeriesVisibility() {
  const root = workbench()
  if (!root) return
  const hidden = hiddenSeries()
  root.querySelectorAll('[data-series-toggle]').forEach((button) => {
    const isVisible = !hidden.has(button.dataset.seriesToggle)
    button.setAttribute('aria-pressed', String(isVisible))
  })
  root.querySelectorAll('[data-series-path]').forEach((path) => {
    path.toggleAttribute('hidden', hidden.has(path.dataset.seriesPath))
  })
}

document.addEventListener('click', (event) => {
  const button = event.target.closest('[data-series-toggle]')
  if (!button || !workbench()?.contains(button)) return
  const hidden = hiddenSeries()
  const key = button.dataset.seriesToggle
  if (hidden.has(key)) hidden.delete(key)
  else hidden.add(key)
  saveHidden(hidden)
  applySeriesVisibility()
})
