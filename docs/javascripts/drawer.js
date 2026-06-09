/* kagents · brand drawer toggle
 *
 * Material's drawer pattern is locked to viewport <76.25em. Above
 * that breakpoint, Material's compiled CSS opens the sidebar inline
 * and the `[data-md-toggle="drawer"]:checked` open-state selector is
 * wrapped inside a max-width media query — so CSS overrides can never
 * make the slide-out work at desktop.
 *
 * Bypass: own the open state with a `data-brand-drawer` attribute on
 * <html>, toggled here. The CSS in brand.css keys off that attribute
 * so the cascade fight with Material's compiled stylesheet doesn't
 * happen at all.
 *
 * Trigger surfaces:
 *   - clicking the hamburger label (any width)
 *   - clicking the overlay (dismiss)
 *   - hitting Escape (dismiss)
 *
 * Idempotent: re-running this script is a no-op because every event
 * listener is added against a single matched element by selector. */

(function() {
  function init() {
    var hamburger = document.querySelector('label[for="__drawer"].md-header__button');
    var overlay = document.querySelector('label.md-overlay');
    if (!hamburger) {
      return;
    }
    if (hamburger.dataset.brandWired === '1') {
      return;
    }
    hamburger.dataset.brandWired = '1';

    function isOpen() {
      return document.documentElement.dataset.brandDrawer === 'open';
    }
    function open() {
      document.documentElement.dataset.brandDrawer = 'open';
    }
    function close() {
      delete document.documentElement.dataset.brandDrawer;
    }

    hamburger.addEventListener('click', function(e) {
      // Material's checkbox-based mobile drawer would fire on its own
      // below the breakpoint. Cancel its default to avoid double-
      // toggling, then drive open-state ourselves.
      e.preventDefault();
      e.stopPropagation();
      if (isOpen()) {
        close();
      } else {
        open();
      }
    });

    if (overlay) {
      overlay.addEventListener('click', function(e) {
        e.preventDefault();
        close();
      });
    }

    document.addEventListener('keydown', function(e) {
      if (e.key === 'Escape' && isOpen()) {
        close();
      }
    });

    // Material's `navigation.instant` feature re-renders the page in
    // place on internal navigations, which can detach our handlers.
    // Re-wire after each instant nav so the drawer keeps working.
    document.addEventListener('DOMContentLoaded', init);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
