// Progressive enhancement: copy buttons for code blocks. The page works without this file.
(function () {
  if (!navigator.clipboard || !window.isSecureContext) return;
  var icon = '<svg class="icon icon-sm" viewBox="0 0 24 24" aria-hidden="true"><rect width="14" height="14" x="8" y="8" rx="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>';
  document.querySelectorAll('.code').forEach(function (box) {
    var bar = box.querySelector('.code-bar');
    var pre = box.querySelector('pre');
    if (!bar || !pre) return;
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'copy';
    btn.innerHTML = icon + '<span>Copy</span>';
    var label = btn.querySelector('span');
    var status = document.createElement('span');
    status.className = 'sr-only';
    status.setAttribute('role', 'status');
    var timer;
    btn.addEventListener('click', function () {
      navigator.clipboard.writeText(pre.textContent.replace(/\s+$/, '') + '\n').then(function () {
        label.textContent = 'Copied';
        status.textContent = 'Copied to clipboard';
      }, function () {
        label.textContent = 'Press Ctrl+C';
        status.textContent = 'Copy failed. Select the text and copy it.';
      });
      clearTimeout(timer);
      timer = setTimeout(function () { label.textContent = 'Copy'; status.textContent = ''; }, 2000);
    });
    bar.appendChild(btn);
    bar.appendChild(status);
  });
})();
