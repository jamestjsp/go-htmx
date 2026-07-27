/* ===============================================================
   Process Lab — the drawing register at `/`.

   The register is deliberately thin. Rows expand through native
   <details>/<summary> with the flowsheets already in the DOM, and
   every mutation is an htmx attribute in the markup. This file
   exists for the three things markup cannot state:

     1. Renaming a project in place, which is a mode the row enters
        and leaves.
     2. Keeping the row from toggling underneath a control the
        pointer actually landed on.
     3. Reporting a refusal, since htmx does not swap a 4xx.

   It is a separate file because the Content-Security-Policy sets
   `script-src 'self'` with no 'unsafe-inline': an inline <script>
   or an onclick= attribute is dropped silently in the browser
   while every Go test still passes.
   =============================================================== */

;(() => {
  'use strict'

  // A double-click has to be told apart from a single click on the same
  // element, and the only way to do that is to wait. 200ms is under the
  // threshold where a delay reads as a hang, and the click is opening a
  // whole page, which takes considerably longer than that anyway.
  const DOUBLE_CLICK_WINDOW = 200

  const rows = () => document.querySelectorAll('.register-row')
  const rowOf = (node) => (node && node.closest ? node.closest('.register-row') : null)

  let pendingOpen = null
  let openBeforeSwap = new Map()

  /* --------------------------------------------------------------
     Renaming in place.
     The input is rendered by the server carrying the stored name as
     its defaultValue, so reverting is `value = defaultValue` and needs
     no separate copy of the name to fall out of date.
     -------------------------------------------------------------- */

  function startRename(row) {
    const input = row.querySelector('.project-rename')
    if (!input) return
    armRenameKeys(input)
    input.value = input.defaultValue
    input.hidden = false
    row.dataset.editing = 'true'
    input.focus()
    input.select()
  }

  // The field's keys are handled on the field, not delegated from the
  // document, because Space and Enter have to be kept away from the <summary>
  // the field sits inside: a <summary> acts on both as they pass it on their
  // way up, so a project name with a space in it would fold the row shut
  // while it was being typed. Stopping the event at the field is the only
  // place that works — a delegate on the document runs long after the row has
  // already reacted.
  //
  // All three of keydown, keypress and keyup are handled, because Chrome
  // toggles the row from the space *keyup*, and it does so from the element's
  // default handler rather than a listener — a walk up the event path that
  // ignores stopPropagation entirely and stops only at a cancelled event.
  // So the keys are both stopped and cancelled. Cancelling a Space or Enter
  // keyup costs a text field nothing: its text arrives on the way down.
  const RENAME_KEY_EVENTS = ['keydown', 'keypress', 'keyup']

  function armRenameKeys(input) {
    if (input.dataset.keysArmed) return
    input.dataset.keysArmed = 'true'
    RENAME_KEY_EVENTS.forEach((type) => input.addEventListener(type, onRenameKey))
  }

  function onRenameKey(event) {
    const input = event.currentTarget
    if (event.key === ' ' || event.key === 'Enter') {
      event.stopPropagation()
      if (event.type === 'keyup') event.preventDefault()
    }
    if (event.type !== 'keydown') return
    if (event.key === 'Escape') {
      event.preventDefault()
      cancelRename(rowOf(input), true)
      return
    }
    if (event.key === 'Enter') {
      // The commit. htmx is listening on the field for `register:rename`
      // rather than `keyup[key=='Enter']`, because compiling that filter needs
      // `new Function` and the page's CSP has no 'unsafe-eval' — the filter
      // would be dropped in the browser and nowhere else.
      event.preventDefault()
      input.dispatchEvent(new CustomEvent('register:rename'))
    }
  }

  function stopRename(row) {
    const input = row.querySelector('.project-rename')
    if (!input) return
    input.value = input.defaultValue
    input.hidden = true
    delete row.dataset.editing
  }

  function cancelRename(row, restoreFocus) {
    stopRename(row)
    const link = row.querySelector('.project-open')
    if (restoreFocus && link) link.focus()
  }

  // The Rename button is the discoverable, keyboard-reachable path to the
  // same mode the double-click opens.
  document.addEventListener('click', (event) => {
    const button = event.target.closest('[data-rename]')
    if (!button) return
    const row = rowOf(button)
    if (row) startRename(row)
  })

  document.addEventListener('dblclick', (event) => {
    const cell = event.target.closest('.line-name')
    if (!cell) return
    const row = rowOf(cell)
    if (!row || row.dataset.editing) return
    event.preventDefault()
    clearTimeout(pendingOpen)
    pendingOpen = null
    startRename(row)
  })

  // Opening the project is the row's primary act, and it must not be lost to
  // the rename gesture that shares its target: a plain click is held for one
  // double-click window, and a second click cancels it.
  //
  // Only a plain primary click pays that wait. Keyboard activation arrives
  // with detail 0 and cannot be half of a double-click; a middle click and
  // every modified click are how a reader opens the project in another tab or
  // window. All of those navigate exactly as they always would.
  document.addEventListener('click', (event) => {
    const link = event.target.closest('.project-open')
    if (!link) return
    if (event.detail === 0 || event.button !== 0) return
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    if (event.detail > 1) return // the closing half of a rename gesture
    const href = link.href
    clearTimeout(pendingOpen)
    pendingOpen = setTimeout(() => {
      pendingOpen = null
      window.location.assign(href)
    }, DOUBLE_CLICK_WINDOW)
  })

  /* --------------------------------------------------------------
     Keeping the row still.
     A click on a link or a button inside the <summary> is that
     control's own: the DOM picks the innermost element with an
     activation behaviour, so the row does not toggle. A text input
     has no activation behaviour, so a click meant for the caret of
     the rename field would toggle the row underneath it. Cancelling
     the click's default action stops the toggle; the caret was
     already placed on mousedown, so nothing is lost.
     -------------------------------------------------------------- */
  document.addEventListener('click', (event) => {
    if (event.target.closest('.project-rename')) event.preventDefault()
  })

  // Clicking away abandons an edit rather than leaving a stray field open on
  // a row the reader has moved on from. A rename in flight is left alone:
  // htmx blurs nothing, but a swap can land after the field loses focus.
  document.addEventListener('focusout', (event) => {
    const input = event.target.closest ? event.target.closest('.project-rename') : null
    if (!input) return
    const row = rowOf(input)
    setTimeout(() => {
      if (row && row.isConnected && !row.contains(document.activeElement)) stopRename(row)
    }, 0)
  })

  /* --------------------------------------------------------------
     Swaps. A rename answers with the row, so the row's expanded
     state has to survive being replaced.
     -------------------------------------------------------------- */

  function rememberOpenRows() {
    openBeforeSwap = new Map()
    rows().forEach((row) => openBeforeSwap.set(row.dataset.project, row.open))
  }

  function restoreOpenRows() {
    openBeforeSwap.forEach((open, id) => {
      const row = document.querySelector('.register-row[data-project="' + id + '"]')
      if (row) row.open = open
    })
    openBeforeSwap = new Map()
  }

  /* --------------------------------------------------------------
     Refusals. htmx does not swap a 4xx, so without this a rejected
     name would look like nothing happened at all.
     -------------------------------------------------------------- */

  const alertBox = () => document.getElementById('register-alert')

  function showAlert(message) {
    const box = alertBox()
    if (!box) return
    box.textContent = message
    box.hidden = false
  }

  function clearAlert() {
    const box = alertBox()
    if (box) box.hidden = true
  }

  document.addEventListener('htmx:beforeRequest', () => {
    clearAlert()
    rememberOpenRows()
  })

  document.addEventListener('htmx:afterSwap', restoreOpenRows)

  document.addEventListener('htmx:responseError', (event) => {
    const xhr = event.detail && event.detail.xhr
    const message = xhr && xhr.responseText ? xhr.responseText.trim() : ''
    showAlert(message || 'The register could not complete that change.')
    const row = rowOf(event.detail && event.detail.elt)
    if (row) cancelRename(row, true)
  })

  /* --------------------------------------------------------------
     The housing bar's + New project walks the reader to the
     register's next free line, which is where a project is actually
     named. Without this the anchor still gets them there.
     -------------------------------------------------------------- */
  document.addEventListener('click', (event) => {
    const jump = event.target.closest('[data-focus-new-project]')
    if (!jump) return
    const input = document.getElementById('new-project-name')
    if (!input) return
    event.preventDefault()
    input.focus()
    const smooth = !window.matchMedia('(prefers-reduced-motion: reduce)').matches
    input.scrollIntoView({ block: 'nearest', behavior: smooth ? 'smooth' : 'auto' })
  })
})()
