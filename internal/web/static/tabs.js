// =====================================================================
// Flowsheet tab strip.
//
// The strip is server-rendered and comes back whole with every swap, so
// this file owns only what the markup cannot state: how far the track is
// scrolled, whether the ‹ › arrows have anywhere left to go, which tab is
// being renamed, and where a dragged tab would land.
//
// Loaded after menu.js and app.js, both of which it uses: menu.js for the
// right-click menu, and htmx for every mutation except the reorder, which
// answers 204 and so has nothing for htmx to swap.
//
// Two constraints shape the code more than anything else:
//
//   - The CSP sets script-src 'self' with no 'unsafe-eval', so htmx's
//     hx-trigger FILTER syntax (`keyup[key=='Enter']`) is compiled with
//     new Function and is dead in the browser while every Go test passes.
//     Every key and pointer gesture below is therefore plain JavaScript.
//   - Any swap can replace the strip mid-gesture. Nothing here holds a
//     tab node across a request; the flowsheet id is the identity, and
//     the node is looked up again on the other side.
// =====================================================================
(() => {
  const ProcessLab = window.ProcessLab || (window.ProcessLab = {})
  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')

  // Set when a new sheet is created, read on the page that the creation
  // redirects to: POST /projects/{id}/flows answers with a redirect, so
  // "open the new tab in rename" has to survive a navigation.
  const NEW_SHEET_KEY = 'processlab:name-new-sheet'
  const NEW_SHEET_WINDOW = 15000

  const workbench = () => document.querySelector('#workbench')
  const tabStrip = () => document.querySelector('#flow-tabs')
  const tabTrack = () => document.querySelector('#flow-tab-track')
  const tabList = () => document.querySelector('#flow-tab-track .tab-list')
  const tabNodes = () => Array.from(document.querySelectorAll('#flow-tab-track [data-flow-tab]'))
  const activeTab = () => document.querySelector('#flow-tab-track [data-flow-tab][aria-current="page"]')
  const tabFor = (id) => document.querySelector(`#flow-tab-track [data-flow-tab="${id}"]`)
  const tabName = (tab) => tab.querySelector('.flow-tab-name').textContent

  // =================================================================
  // Overflow: the ‹ › arrows and keeping the open sheet in view.
  // =================================================================

  // Where a tab starts within the scrolled content. offsetLeft would be
  // measured from the offset parent, which is not the track.
  function tabOffsets(track) {
    const origin = track.getBoundingClientRect().left - track.scrollLeft
    return Array.from(track.querySelectorAll('[data-flow-tab]')).map((tab) => {
      const box = tab.getBoundingClientRect()
      return { start: box.left - origin, end: box.right - origin }
    })
  }

  function syncTabStrip() {
    const strip = tabStrip()
    const track = tabTrack()
    if (!strip || !track) return
    const slack = track.scrollWidth - track.clientWidth
    strip.dataset.overflow = slack > 1 ? 'true' : 'false'
    const back = strip.querySelector('[data-tab-scroll="-1"]')
    const forward = strip.querySelector('[data-tab-scroll="1"]')
    if (back) back.disabled = track.scrollLeft <= 1
    if (forward) forward.disabled = track.scrollLeft >= slack - 1
  }

  // One tab per press, the way a workbook steps its sheet tabs: the leftmost
  // tab still in view is the anchor, and an arrow moves the anchor by one.
  // Scrolling by a fraction of the window instead would step by half a name.
  function scrollTabs(direction) {
    const track = tabTrack()
    if (!track) return
    const tabs = tabOffsets(track)
    if (!tabs.length) return
    let anchor = tabs.findIndex((tab) => tab.end > track.scrollLeft + 1)
    if (anchor < 0) anchor = tabs.length - 1
    const next = tabs[Math.min(tabs.length - 1, Math.max(0, anchor + direction))]
    track.scrollTo({
      left: Math.max(0, next.start),
      behavior: reducedMotion.matches ? 'auto' : 'smooth'
    })
  }

  // Every swap rebuilds the strip with its scroll at zero, so the sheet you
  // just opened has to be brought back into view.
  function revealActiveTab() {
    const track = tabTrack()
    if (!track) return
    const active = activeTab()
    if (!active) return
    const view = track.getBoundingClientRect()
    const tab = active.getBoundingClientRect()
    const margin = 16
    if (tab.left < view.left) {
      track.scrollTo({ left: track.scrollLeft - (view.left - tab.left) - margin, behavior: 'auto' })
    } else if (tab.right > view.right) {
      track.scrollTo({ left: track.scrollLeft + (tab.right - view.right) + margin, behavior: 'auto' })
    }
  }

  // =================================================================
  // Failures.
  //
  // Every refusal on this strip is the domain's own sentence, raised in
  // the banner the server-rendered workbench uses for the same purpose,
  // so a rejected rename and a rejected block edit read alike.
  // =================================================================
  function raiseBanner(message) {
    const root = workbench()
    const topbar = root && root.querySelector('.topbar')
    if (!topbar) return
    root.querySelectorAll('.error-banner').forEach((banner) => banner.remove())
    const banner = document.createElement('section')
    banner.className = 'error-banner'
    banner.setAttribute('role', 'alert')
    banner.innerHTML =
      '<span>!</span><div><strong>Flowsheet needs attention</strong><p></p></div>' +
      '<button type="button" data-dismiss-error aria-label="Dismiss error">×</button>'
    // textContent, not innerHTML: the message is a server response body.
    banner.querySelector('p').textContent =
      String(message || '').trim() || 'The operation could not be completed.'
    topbar.after(banner)
  }

  // =================================================================
  // Inline rename.
  //
  // The label is swapped for a field inside the tab it belongs to, rather
  // than a dialog: the name is being edited where it is read.
  //
  // The field lives inside an <a>, which wants to navigate on click and
  // to start a native drag on pointerdown. Both are suppressed at the
  // field, and the click needs preventDefault as well as stopPropagation
  // — Blink runs a link's activation behaviour by walking the event path,
  // which stopping propagation does not touch.
  // =================================================================
  let editor = null       // { tab, input, label, original, id }
  let renamingID = ''     // survives a swap; see settle()
  let lastRename = null   // { id, original } — what a 4xx has to put back
  let carried = null      // { id, value } — text rescued from a swapped-away field
  let swapping = false

  // seed is the half-typed name being carried across a swap; without one
  // the field opens on the name the tab is showing.
  function beginRename(tab, seed) {
    if (!tab || !tab.isConnected) return
    if (editor && editor.tab === tab) return
    endRename(false)
    const label = tab.querySelector('.flow-tab-name')
    if (!label) return

    const original = label.textContent
    const input = document.createElement('input')
    input.type = 'text'
    input.className = 'flow-tab-input'
    input.value = seed === undefined ? original : seed
    input.maxLength = 80
    input.autocomplete = 'off'
    input.spellcheck = false
    input.setAttribute('aria-label', 'Flowsheet name')
    // Measured before the label is hidden, so the strip does not jump as
    // the tab turns editable.
    input.style.width = `${fieldWidth(label.offsetWidth)}px`

    label.hidden = true
    tab.dataset.renaming = ''
    label.after(input)
    editor = { tab, input, label, original, id: tab.dataset.flowTab }
    renamingID = editor.id

    input.addEventListener('input', () => {
      input.style.width = `${fieldWidth(measure(input))}px`
    })
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') {
        event.preventDefault()
        event.stopPropagation()
        endRename(true)
      } else if (event.key === 'Escape') {
        event.preventDefault()
        event.stopPropagation()
        const host = editor && editor.tab
        endRename(false)
        if (host && host.isConnected) host.focus()
      }
    })
    // Committing on blur is the workbook's own rule: clicking away keeps
    // what you typed.
    //
    // Not during a swap, though, and this is the whole double-click path.
    // The first click of the double-click opens the sheet, so the strip is
    // replaced about ten milliseconds after the field is created; Blink
    // blurs the field on the way out while it is still connected. Treating
    // that as "the user left the field" ends the rename that the gesture
    // has only just asked for, and the request is what settle() re-attaches
    // on the other side of the swap.
    input.addEventListener('blur', () => {
      if (swapping) return
      if (editor && editor.input === input && input.isConnected) endRename(true)
    })
    const swallow = (event) => event.stopPropagation()
    input.addEventListener('pointerdown', swallow)
    input.addEventListener('mousedown', swallow)
    input.addEventListener('dblclick', swallow)
    input.addEventListener('click', (event) => {
      event.stopPropagation()
      event.preventDefault()
    })

    input.focus()
    input.select()
  }

  const fieldWidth = (width) => Math.min(208, Math.max(84, Math.round(width) + 22))

  // The field grows with the name, up to the width a tab is allowed. A
  // canvas ruler rather than the field's own scrollWidth, which reports
  // the padding box back once the text fits and would make the field
  // widen by its own padding on every keystroke.
  let ruler = null

  function measure(input) {
    if (!ruler) ruler = document.createElement('canvas').getContext('2d')
    const style = window.getComputedStyle(input)
    ruler.font = `${style.fontWeight} ${style.fontSize} ${style.fontFamily}`
    return ruler.measureText(input.value).width
  }

  function endRename(commit) {
    if (!editor) return
    const session = editor
    editor = null
    renamingID = ''
    carried = null
    session.input.remove()
    session.label.hidden = false
    delete session.tab.dataset.renaming

    const typed = session.input.value
    if (!commit || typed.trim() === session.original.trim()) return
    // Drawn straight away so the name does not flicker back to the old one
    // while the request is in flight; a refusal puts it back.
    session.label.textContent = typed.trim()
    lastRename = { id: session.id, original: session.original }
    const values = { name: typed }
    const selected = workbench() && workbench().dataset.selectedId
    if (selected) values.selected_id = selected
    request('PUT', `/flows/${session.id}/name`, values)
  }

  // Rename is asked for by identity, not by node: the double-click that
  // asks for it also opens the sheet, so the tab is usually replaced by
  // the swap that arrives a moment later. settle() re-attaches the field
  // to whatever node carries that id on the other side.
  function requestRename(id) {
    renamingID = String(id)
    beginRename(tabFor(renamingID))
  }

  // =================================================================
  // Duplicate and delete.
  //
  // Both answer with the workbench fragment already opened where the
  // caller should land — the copy, and the neighbour the domain chose —
  // so the client swaps the response and navigates nowhere itself.
  // =================================================================
  let addressPending = false

  function duplicateFlow(id) {
    addressPending = true
    request('POST', `/flows/${id}/duplicate`)
  }

  function deleteFlow(id, name) {
    const question = `Delete “${name}”? Its blocks, signal wires and run history go with it.`
    if (!window.confirm(question)) return
    addressPending = true
    request('DELETE', `/flows/${id}`)
  }

  function request(verb, path, values) {
    return htmx.ajax(verb, path, {
      target: '#workbench',
      swap: 'outerHTML',
      values: values || {}
    })
  }

  // The last tab standing has no Delete. The domain refuses it too — this
  // is the rule made unreachable, not the rule itself.
  ProcessLab.menu.register({
    selector: '[data-flow-tab]',
    items: (event, host) => {
      const id = host.dataset.flowTab
      const name = tabName(host)
      const items = [
        { label: 'Rename', run: () => requestRename(id) },
        { label: 'Duplicate', run: () => duplicateFlow(id) }
      ]
      if (tabNodes().length > 1) {
        items.push({ label: 'Delete sheet', run: () => deleteFlow(id, name), danger: true })
      }
      return items
    }
  })

  // =================================================================
  // Reorder: dragging, and Ctrl/Cmd + Shift + arrow.
  //
  // PATCH /projects/{id}/flows/order answers 204 with an empty body, so
  // there is nothing to swap on success — the client has already drawn
  // the order it is reporting. A refusal is the one case where the strip
  // on screen and the strip in the database can disagree, and the server
  // is the one that is right, so the workbench fragment is re-requested
  // rather than the drag being undone from memory.
  // =================================================================
  let drag = null
  let marker = null
  let autoScroll = 0
  let suppressClick = false

  function orderNow() {
    return tabNodes().map((tab) => tab.dataset.flowTab)
  }

  function commitOrder(before) {
    const strip = tabStrip()
    const after = orderNow()
    if (!strip || after.join() === before.join()) return
    const body = new URLSearchParams()
    after.forEach((id) => body.append('id', id))
    fetch(`/projects/${strip.dataset.projectId}/flows/order`, { method: 'PATCH', body })
      .then((response) => {
        if (response.status === 204) return null
        return response.text().then((text) => resync(text))
      })
      .catch(() => resync('Process Lab could not save the new tab order.'))
  }

  function resync(message) {
    const root = workbench()
    if (!root) return
    return request('GET', `/flows/${root.dataset.flowId}/workbench`)
      .then(() => raiseBanner(message))
  }

  function showMarker(index, tabs) {
    const list = tabList()
    if (!list || !tabs.length) return
    if (!marker || !marker.isConnected) {
      marker = document.createElement('i')
      marker.className = 'tab-insert'
      marker.setAttribute('aria-hidden', 'true')
      list.appendChild(marker)
    }
    const at = tabs[index]
    const last = tabs[tabs.length - 1]
    marker.style.left = `${at ? at.offsetLeft : last.offsetLeft + last.offsetWidth}px`
  }

  function clearMarker() {
    if (marker) marker.remove()
    marker = null
  }

  // The tabs the dragged one could land between, and where in that list
  // the pointer currently sits: before the first tab whose midpoint the
  // pointer has not passed, and last when it has passed them all.
  //
  // The dragged tab is an argument rather than read off the drag session,
  // because the drop is resolved after that session has been closed out.
  function dropTarget(clientX, dragged) {
    const others = tabNodes().filter((tab) => tab !== dragged)
    let index = others.length
    for (let i = 0; i < others.length; i += 1) {
      const box = others[i].getBoundingClientRect()
      if (clientX < box.left + box.width / 2) {
        index = i
        break
      }
    }
    return { others, index }
  }

  // A strip wider than the window has to come to the pointer, or the last
  // few sheets are places a drag cannot reach.
  function edgeScroll(clientX) {
    const track = tabTrack()
    if (!track) return
    const box = track.getBoundingClientRect()
    const zone = 44
    if (clientX < box.left + zone) autoScroll = -12
    else if (clientX > box.right - zone) autoScroll = 12
    else autoScroll = 0
  }

  function stepAutoScroll() {
    if (!drag) return
    const track = tabTrack()
    if (track && autoScroll) {
      track.scrollLeft += autoScroll
      const target = dropTarget(drag.clientX, drag.tab)
      showMarker(target.index, target.others)
    }
    window.requestAnimationFrame(stepAutoScroll)
  }

  document.addEventListener('pointerdown', (event) => {
    if (event.button !== 0 || event.target.closest('.flow-tab-input')) return
    const tab = event.target.closest('#flow-tab-track [data-flow-tab]')
    if (!tab) return
    drag = { tab, startX: event.clientX, clientX: event.clientX, moved: false }
  })

  document.addEventListener('pointermove', (event) => {
    if (!drag) return
    drag.clientX = event.clientX
    if (!drag.moved) {
      // A few pixels of slop, so a click that trembles still opens the
      // sheet instead of starting a reorder.
      if (Math.abs(event.clientX - drag.startX) < 4) return
      drag.moved = true
      drag.before = orderNow()
      tabStrip().dataset.dragging = 'true'
      drag.tab.dataset.dragging = ''
      window.requestAnimationFrame(stepAutoScroll)
    }
    event.preventDefault()
    const target = dropTarget(event.clientX, drag.tab)
    showMarker(target.index, target.others)
    edgeScroll(event.clientX)
  })

  function endDrag(event, drop) {
    if (!drag) return
    const session = drag
    drag = null
    autoScroll = 0
    clearMarker()
    delete session.tab.dataset.dragging
    const strip = tabStrip()
    if (strip) delete strip.dataset.dragging
    if (!session.moved) return
    // The gesture was a drag, so the click that follows the release must
    // not also open the sheet the tab was dropped on.
    suppressClick = true
    window.setTimeout(() => { suppressClick = false }, 0)
    if (!drop) return
    const list = tabList()
    const target = dropTarget(event.clientX, session.tab)
    if (list) list.insertBefore(session.tab, target.others[target.index] || null)
    revealActiveTab()
    syncTabStrip()
    commitOrder(session.before)
  }

  document.addEventListener('pointerup', (event) => endDrag(event, true))
  // A button released outside the window may never come back as a
  // pointerup, and a drag left running would keep the marker up and steal
  // the next click on the strip.
  window.addEventListener('blur', () => endDrag(null, false))
  document.addEventListener('pointercancel', (event) => endDrag(event, false))

  document.addEventListener('click', (event) => {
    if (!suppressClick) return
    if (!event.target.closest('#flow-tab-track [data-flow-tab]')) return
    suppressClick = false
    event.preventDefault()
    event.stopPropagation()
  }, true)

  document.addEventListener('keydown', (event) => {
    if (!event.shiftKey || !(event.ctrlKey || event.metaKey)) return
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
    if (editor || event.target.closest('input, textarea, select, [contenteditable="true"]')) return
    if (ProcessLab.menu.ownsKey(event)) return
    const list = tabList()
    const active = activeTab()
    if (!list || !active) return
    const tabs = tabNodes()
    const to = tabs.indexOf(active) + (event.key === 'ArrowLeft' ? -1 : 1)
    if (to < 0 || to >= tabs.length) return
    event.preventDefault()
    const before = orderNow()
    // Moving right means landing after the tab being passed, which is the
    // one place insertBefore needs its neighbour rather than its target.
    list.insertBefore(active, event.key === 'ArrowLeft' ? tabs[to] : tabs[to].nextSibling)
    revealActiveTab()
    syncTabStrip()
    commitOrder(before)
  })

  // =================================================================
  // Wiring.
  // =================================================================
  document.addEventListener('dblclick', (event) => {
    if (event.target.closest('.flow-tab-input')) return
    const tab = event.target.closest('#flow-tab-track [data-flow-tab]')
    if (!tab) return
    event.preventDefault()
    requestRename(tab.dataset.flowTab)
  })

  document.addEventListener('click', (event) => {
    const arrow = event.target.closest('[data-tab-scroll]')
    if (arrow) scrollTabs(Number(arrow.dataset.tabScroll))
  })

  // A new sheet is created by a form post that redirects onto it, so the
  // request to name it has to be left where the next page will find it.
  document.addEventListener('submit', (event) => {
    if (!event.target.closest('.tab-add-form')) return
    try {
      window.sessionStorage.setItem(NEW_SHEET_KEY, String(Date.now()))
    } catch (error) {
      /* storage disabled; the sheet is still created, it just opens named */
    }
  })

  function nameNewSheet() {
    let stamp = null
    try {
      stamp = window.sessionStorage.getItem(NEW_SHEET_KEY)
      window.sessionStorage.removeItem(NEW_SHEET_KEY)
    } catch (error) {
      return
    }
    // A creation that failed leaves the flag behind, so it is only honoured
    // while it is fresh enough to belong to this navigation.
    if (!stamp || Date.now() - Number(stamp) > NEW_SHEET_WINDOW) return
    const active = activeTab()
    if (active) requestRename(active.dataset.flowTab)
  }

  // Refusals from the three htmx-borne operations. The rename is the one
  // that has something to put back: the label was drawn optimistically.
  //
  // As shipped, PUT /flows/{id}/name answers a refusal with 200 and the
  // workbench fragment carrying the banner, so this listener does not fire
  // for it and the swap does the reverting. It is here because the route
  // is free to answer 4xx — the other two do — and a name left showing on
  // a tab the server never accepted is the worst outcome on this strip.
  document.addEventListener('htmx:responseError', (event) => {
    const path = (event.detail.pathInfo && event.detail.pathInfo.requestPath) || ''
    const rename = /^\/flows\/(\d+)\/name$/.exec(path)
    if (!rename && !/^\/flows\/\d+(\/duplicate)?$/.test(path)) return
    if (rename && lastRename && lastRename.id === rename[1]) {
      const tab = tabFor(lastRename.id)
      if (tab) tab.querySelector('.flow-tab-name').textContent = lastRename.original
    }
    lastRename = null
    raiseBanner(event.detail.xhr && event.detail.xhr.responseText)
  })

  // The address bar after a duplicate or a delete. Both answer with the
  // workbench fragment for a DIFFERENT flowsheet — the copy, the
  // neighbour — and neither is a navigation htmx pushed a URL for, so
  // without this a reload would ask for the sheet you were on before, and
  // after a delete that sheet is gone.
  //
  // Only after those two. Replacing the entry htmx is itself pushing for
  // an ordinary tab click would put the same address in history twice.
  function syncAddress() {
    if (!addressPending) return
    addressPending = false
    const active = activeTab()
    const href = active && active.getAttribute('href')
    if (href && href !== window.location.pathname) {
      window.history.replaceState(window.history.state, '', href)
    }
  }

  // A field open when a swap begins is about to be discarded along with
  // the strip it sits in, so what was typed into it is kept here for
  // settle() to put back.
  document.addEventListener('htmx:beforeSwap', () => {
    swapping = true
    carried = editor && editor.input.isConnected
      ? { id: editor.id, value: editor.input.value }
      : null
  })

  const settle = (settled) => {
    swapping = false
    revealActiveTab()
    syncTabStrip()
    // At afterSwap the incoming markup is not yet what querySelector
    // returns, so the address is read from the settled DOM only.
    if (settled) syncAddress()
    // A swap that lands mid-rename discards the field; the request for it
    // is by flowsheet id, so it is re-opened on the tab that replaced it.
    if (!renamingID || (editor && editor.input.isConnected)) return
    const tab = tabFor(renamingID)
    if (tab) beginRename(tab, carried && carried.id === renamingID ? carried.value : undefined)
    else if (settled) renamingID = ''
  }

  // scroll does not bubble, and the track is replaced on every swap, so the
  // capture phase is what keeps this to a single listener.
  document.addEventListener('scroll', (event) => {
    if (event.target === tabTrack()) syncTabStrip()
  }, true)
  document.addEventListener('htmx:afterSwap', () => settle(false))
  document.addEventListener('htmx:afterSettle', () => settle(true))
  document.addEventListener('htmx:historyRestore', () => settle(true))
  window.addEventListener('resize', syncTabStrip)

  revealActiveTab()
  syncTabStrip()
  nameNewSheet()
})()
