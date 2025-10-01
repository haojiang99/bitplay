<script>
  import { onMount } from 'svelte';

  let movies = [];
  let loading = true;
  let error = null;
  let currentPage = 1;
  let totalPages = 1;
  let searchQuery = '';
  let activeSearch = '';

  async function fetchMovies(page = 1, query = '') {
    loading = true;
    error = null;

    try {
      let url = `/api/v1/yts/movies?page=${page}`;
      if (query && query.trim()) {
        url += `&query=${encodeURIComponent(query.trim())}`;
      }

      const response = await fetch(url);
      if (!response.ok) {
        throw new Error('Failed to fetch movies');
      }

      const data = await response.json();

      if (data.status === 'ok' && data.data && data.data.movies) {
        movies = data.data.movies;
        currentPage = page;
        activeSearch = query;

        // Calculate total pages
        if (data.data.movie_count && data.data.limit) {
          totalPages = Math.ceil(data.data.movie_count / data.data.limit);
        }
      } else {
        movies = [];
      }
    } catch (err) {
      error = err.message;
    } finally {
      loading = false;
    }
  }

  function handleSearch(event) {
    event.preventDefault();
    currentPage = 1;
    fetchMovies(1, searchQuery);
  }

  function clearSearch() {
    searchQuery = '';
    activeSearch = '';
    currentPage = 1;
    fetchMovies(1, '');
  }

  let showModal = false;
  let currentMagnetLink = '';
  let isLoading = true;

  function playMovie(torrent) {
    // Get the magnet link (constructed from hash in backend)
    const magnetLink = torrent.magnetUrl || torrent.url;

    if (!magnetLink || !magnetLink.startsWith('magnet:')) {
      alert('Invalid torrent link');
      return;
    }

    // Store the magnet link and show modal with loading state
    currentMagnetLink = magnetLink;
    showModal = true;
    isLoading = true;

    // Wait for modal to render, then trigger video loading
    setTimeout(() => {
      const magnetInput = document.getElementById('magnet');
      if (magnetInput) {
        magnetInput.value = magnetLink;

        // Trigger the form submission
        const form = document.getElementById('torrent-form');
        if (form) {
          form.dispatchEvent(new Event('submit'));
        }

        // Listen for when video starts playing to hide loading indicator
        setTimeout(() => {
          const videoPlayer = document.getElementById('video-player');
          if (videoPlayer) {
            videoPlayer.addEventListener('loadeddata', () => {
              isLoading = false;
            }, { once: true });

            videoPlayer.addEventListener('playing', () => {
              isLoading = false;
            }, { once: true });
          }
        }, 500);
      }
    }, 100);
  }

  function closeModal() {
    showModal = false;
    // Stop video playback
    const videoPlayer = document.getElementById('video-player');
    if (videoPlayer) {
      const player = window.videojs?.(videoPlayer);
      if (player) {
        player.pause();
      }
    }
  }

  function toggleFullscreen() {
    const videoPlayer = document.getElementById('video-player');
    if (videoPlayer) {
      const player = window.videojs?.(videoPlayer);
      if (player) {
        if (player.isFullscreen()) {
          player.exitFullscreen();
        } else {
          player.requestFullscreen();
        }
      }
    }
  }

  function nextPage() {
    if (currentPage < totalPages) {
      fetchMovies(currentPage + 1, activeSearch);
    }
  }

  function prevPage() {
    if (currentPage > 1) {
      fetchMovies(currentPage - 1, activeSearch);
    }
  }

  onMount(() => {
    fetchMovies();
  });
</script>

<div class="movie-browser">
  <h2 class="text-2xl md:text-3xl font-bold mb-4 text-center">Movies</h2>

  <!-- Search Bar -->
  <form on:submit={handleSearch} class="search-form mb-6">
    <div class="search-wrapper">
      <input
        type="text"
        bind:value={searchQuery}
        placeholder="Search movies by title..."
        class="search-input"
      />
      <button type="submit" class="search-button">
        <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="11" cy="11" r="8"></circle>
          <path d="m21 21-4.3-4.3"></path>
        </svg>
        Search
      </button>
      {#if activeSearch}
        <button type="button" on:click={clearSearch} class="clear-button">
          <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18 6 6 18"></path>
            <path d="m6 6 12 12"></path>
          </svg>
          Clear
        </button>
      {/if}
    </div>
    {#if activeSearch}
      <p class="search-info">Showing results for: <strong>{activeSearch}</strong></p>
    {/if}
  </form>

  {#if loading}
    <div class="flex justify-center items-center py-20">
      <div class="loader-spinner"></div>
    </div>
  {:else if error}
    <div class="error-message p-4 mb-4 rounded-lg bg-red-500/10 border border-red-500 text-red-500">
      <p>Error: {error}</p>
    </div>
  {:else if movies.length === 0}
    <div class="no-movies p-4 text-center text-muted-foreground">
      <p>No movies found.</p>
    </div>
  {:else}
    <div class="movies-grid grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-4 mb-6">
      {#each movies as movie}
        <div class="movie-card rounded-lg overflow-hidden border border-border bg-card shadow-sm hover:shadow-lg transition-shadow">
          <div class="movie-poster relative aspect-[2/3] overflow-hidden bg-muted">
            <img
              src={movie.medium_cover_image}
              alt={movie.title}
              class="w-full h-full object-cover"
              loading="lazy"
            />
            <div class="movie-rating absolute top-2 right-2 bg-black/70 text-white px-2 py-1 rounded text-sm font-semibold">
              ⭐ {movie.rating}
            </div>
          </div>
          <div class="movie-info p-3">
            <h3 class="movie-title font-semibold text-sm mb-1 line-clamp-2" title={movie.title}>
              {movie.title}
            </h3>
            <p class="movie-year text-xs text-muted-foreground mb-2">{movie.year}</p>

            {#if movie.torrents && movie.torrents.length > 0}
              <div class="movie-torrents space-y-1">
                {#each movie.torrents as torrent}
                  <button
                    class="torrent-btn w-full text-xs py-1 px-2 rounded bg-primary text-primary-foreground hover:bg-primary/90 transition-colors"
                    on:click={() => playMovie(torrent)}
                  >
                    {torrent.quality} - {torrent.size}
                  </button>
                {/each}
              </div>
            {/if}
          </div>
        </div>
      {/each}
    </div>

    <div class="pagination flex justify-center items-center gap-4 mt-6">
      <button
        class="btn-pagination px-4 py-2 rounded border bg-background hover:bg-accent transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        on:click={prevPage}
        disabled={currentPage === 1}
      >
        Previous
      </button>
      <span class="page-info text-sm">
        Page {currentPage} of {totalPages}
      </span>
      <button
        class="btn-pagination px-4 py-2 rounded border bg-background hover:bg-accent transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        on:click={nextPage}
        disabled={currentPage === totalPages}
      >
        Next
      </button>
    </div>
  {/if}
</div>

<!-- Video Player Modal -->
{#if showModal}
  <div class="modal-overlay" on:click={closeModal}>
    <div class="modal-content" on:click|stopPropagation>
      <div class="modal-header">
        <button class="modal-close" on:click={closeModal} title="Close">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18 6 6 18"></path>
            <path d="m6 6 12 12"></path>
          </svg>
        </button>
        <button class="modal-fullscreen" on:click={toggleFullscreen} title="Toggle Fullscreen">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M8 3H5a2 2 0 0 0-2 2v3"></path>
            <path d="M21 8V5a2 2 0 0 0-2-2h-3"></path>
            <path d="M3 16v3a2 2 0 0 0 2 2h3"></path>
            <path d="M16 21h3a2 2 0 0 0 2-2v-3"></path>
          </svg>
        </button>
      </div>
      <div class="modal-video-container">
        {#if isLoading}
          <div class="loading-overlay">
            <div class="loading-spinner"></div>
            <p class="loading-text">Loading video... Please wait</p>
            <p class="loading-subtext">This may take a few seconds to a few minutes</p>
          </div>
        {/if}
        <div id="modal-video-player-wrapper"></div>
      </div>
    </div>
  </div>
{/if}

<style>
  .movie-browser {
    width: 100%;
    padding: 1rem 0;
  }

  /* Search Bar Styles */
  .search-form {
    max-width: 600px;
    margin: 0 auto;
  }

  .search-wrapper {
    display: flex;
    gap: 0.5rem;
    align-items: center;
  }

  .search-input {
    flex: 1;
    padding: 0.75rem 1rem;
    border: 1px solid hsl(var(--border));
    border-radius: 0.5rem;
    background: hsl(var(--background));
    color: hsl(var(--foreground));
    font-size: 0.875rem;
    transition: all 0.2s;
  }

  .search-input:focus {
    outline: none;
    border-color: hsl(var(--primary));
    box-shadow: 0 0 0 3px hsl(var(--primary) / 0.1);
  }

  .search-input::placeholder {
    color: hsl(var(--muted-foreground));
  }

  .search-button,
  .clear-button {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.75rem 1.25rem;
    border: none;
    border-radius: 0.5rem;
    font-size: 0.875rem;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.2s;
  }

  .search-button {
    background: hsl(var(--primary));
    color: hsl(var(--primary-foreground));
  }

  .search-button:hover {
    background: hsl(var(--primary) / 0.9);
  }

  .clear-button {
    background: hsl(var(--muted));
    color: hsl(var(--foreground));
  }

  .clear-button:hover {
    background: hsl(var(--muted) / 0.8);
  }

  .search-info {
    margin-top: 0.75rem;
    text-align: center;
    color: hsl(var(--muted-foreground));
    font-size: 0.875rem;
  }

  .search-info strong {
    color: hsl(var(--foreground));
    font-weight: 600;
  }

  .loader-spinner {
    border: 3px solid rgba(0, 0, 0, 0.1);
    border-top: 3px solid #3b82f6;
    border-radius: 50%;
    width: 40px;
    height: 40px;
    animation: spin 1s linear infinite;
  }

  @keyframes spin {
    0% { transform: rotate(0deg); }
    100% { transform: rotate(360deg); }
  }

  .line-clamp-2 {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  /* Modal styles */
  .modal-overlay {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background-color: rgba(0, 0, 0, 0.9);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 9999;
    padding: 1rem;
  }

  .modal-content {
    position: relative;
    width: 100%;
    max-width: 1200px;
    max-height: 90vh;
    background-color: #000;
    border-radius: 8px;
    overflow: hidden;
    box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
  }

  .modal-header {
    position: absolute;
    top: 0;
    right: 0;
    z-index: 10;
    display: flex;
    gap: 0.5rem;
    padding: 1rem;
  }

  .modal-close,
  .modal-fullscreen {
    background-color: rgba(0, 0, 0, 0.7);
    border: none;
    border-radius: 50%;
    width: 40px;
    height: 40px;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    color: white;
    transition: background-color 0.2s;
  }

  .modal-close:hover,
  .modal-fullscreen:hover {
    background-color: rgba(0, 0, 0, 0.9);
  }

  .modal-video-container {
    width: 100%;
    aspect-ratio: 16 / 9;
    background-color: #000;
  }

  #modal-video-player-wrapper {
    width: 100%;
    height: 100%;
  }

  .loading-overlay {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    background-color: rgba(0, 0, 0, 0.8);
    z-index: 10;
  }

  .loading-spinner {
    border: 4px solid rgba(255, 255, 255, 0.1);
    border-top: 4px solid #3b82f6;
    border-radius: 50%;
    width: 60px;
    height: 60px;
    animation: spin 1s linear infinite;
    margin-bottom: 1.5rem;
  }

  .loading-text {
    color: white;
    font-size: 1.25rem;
    font-weight: 600;
    margin: 0 0 0.5rem 0;
  }

  .loading-subtext {
    color: rgba(255, 255, 255, 0.7);
    font-size: 0.875rem;
    margin: 0;
  }
</style>
