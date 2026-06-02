(function () {
  try {
    const theme = window.localStorage.getItem('pyttechat.theme');
    if (theme === 'light' || theme === 'dark') {
      document.documentElement.dataset.theme = theme;
    }
  } catch (error) {
    // Keep the system color scheme when browser storage is unavailable.
  }
})();
