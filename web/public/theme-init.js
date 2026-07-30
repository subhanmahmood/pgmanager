// Runs before first paint, as a render-blocking classic script — an inline
// <script> would be blocked by `script-src 'self'`. Without this the page paints
// light and then flips to dark on hydration.
//
// Keep the storage key and the resolution rule in sync with src/lib/theme.tsx.
;(function () {
  try {
    var stored = localStorage.getItem('pgmanager-theme') || 'system'
    var dark =
      stored === 'dark' ||
      (stored === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
    document.documentElement.classList.toggle('dark', dark)
    document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
  } catch (e) {
    /* private mode, disabled storage — fall through to the CSS default */
  }
})()
