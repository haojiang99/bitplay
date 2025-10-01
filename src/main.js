import App from './App.svelte';

function mountApp() {
  const target = document.getElementById('svelte-app');
  if (target) {
    new App({ target });
  }
}

// Wait for DOM to be ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', mountApp);
} else {
  mountApp();
}
