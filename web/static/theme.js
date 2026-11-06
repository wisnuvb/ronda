// Runs before first paint (a blocking script in <head>) so a saved theme never flashes the wrong colours.
try {
  // 'webmon-theme' is the key from before the project was renamed; read it once more so nobody's choice is lost.
  var saved = localStorage.getItem('ronda-theme') || localStorage.getItem('webmon-theme');
  if (saved === 'light' || saved === 'dark') document.documentElement.dataset.theme = saved;
} catch (e) { /* storage blocked: follow the system theme */ }
