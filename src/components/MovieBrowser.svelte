<script>
  import { onMount } from 'svelte';

  let movies = [];
  let loading = true;
  let error = null;
  let currentPage = 1;
  let totalPages = 1;
  let searchQuery = '';
  let activeSearch = '';
  let showFavorites = false;
  let favorites = [];
  let favoritedMovieIds = new Set();
  let selectedFilter = 'like_count'; // Default filter - Most Popular
  let showFilterPanel = false; // Filter panel visibility state

  const filterOptions = [
    { value: 'date_added', label: 'Latest Movies', icon: '🆕' },
    { value: 'download_count', label: 'Most Downloaded', icon: '⬇️' },
    { value: 'like_count', label: 'Most Popular', icon: '🔥' },
    { value: 'rating', label: 'Top Rated', icon: '⭐' },
    { value: 'seeds', label: 'Best Availability', icon: '🌱' },
  ];

  async function fetchMovies(page = 1, query = '', sortBy = selectedFilter) {
    loading = true;
    error = null;

    try {
      let url = `/api/v1/yts/movies?page=${page}&sort_by=${sortBy}&order_by=desc`;
      if (query && query.trim()) {
        url += `&query=${encodeURIComponent(query.trim())}`;
      }

      console.log('Fetching movies with:', { page, query, sortBy, url });

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
    fetchMovies(1, searchQuery, selectedFilter);
  }

  function clearSearch() {
    searchQuery = '';
    activeSearch = '';
    currentPage = 1;
    fetchMovies(1, '', selectedFilter);
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
    isLoading = false;
    // Stop video playback
    const videoPlayer = document.getElementById('video-player');
    if (videoPlayer) {
      const player = window.videojs?.(videoPlayer);
      if (player) {
        player.pause();
      }
    }
  }

  function closeLoadingOverlay() {
    isLoading = false;
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
      fetchMovies(currentPage + 1, activeSearch, selectedFilter);
    }
  }

  function prevPage() {
    if (currentPage > 1) {
      fetchMovies(currentPage - 1, activeSearch, selectedFilter);
    }
  }

  function selectFilter(filterValue) {
    console.log('Filter selected:', filterValue);
    selectedFilter = filterValue;
    currentPage = 1;
    fetchMovies(1, activeSearch, filterValue);
  }

  function toggleFilterPanel() {
    showFilterPanel = !showFilterPanel;
  }

  async function fetchFavorites() {
    try {
      const response = await fetch('/api/v1/favorites');
      if (!response.ok) {
        throw new Error('Failed to fetch favorites');
      }
      const data = await response.json();
      favorites = data || [];
      favoritedMovieIds = new Set(favorites.map(m => m.id));
    } catch (err) {
      console.error('Error fetching favorites:', err);
      favorites = [];
      favoritedMovieIds = new Set();
    }
  }

  async function toggleFavorite(movie) {
    const isFavorited = favoritedMovieIds.has(movie.id);

    try {
      if (isFavorited) {
        // Remove from favorites
        const response = await fetch(`/api/v1/favorites/remove/${movie.id}`, {
          method: 'DELETE',
        });
        if (!response.ok) throw new Error('Failed to remove favorite');
      } else {
        // Add to favorites
        const response = await fetch('/api/v1/favorites/add', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            movie_id: movie.id,
            title: movie.title,
            year: movie.year,
            rating: movie.rating,
            runtime: movie.runtime,
            genres: movie.genres || [],
            summary: movie.summary || '',
            cover_image: movie.medium_cover_image || '',
            torrents: movie.torrents || [],
          }),
        });
        if (!response.ok) throw new Error('Failed to add favorite');
      }
      // Refresh favorites list
      await fetchFavorites();
    } catch (err) {
      console.error('Error toggling favorite:', err);
      alert('Failed to update favorites');
    }
  }

  function openFavorites() {
    showFavorites = true;
  }

  function closeFavorites() {
    showFavorites = false;
  }

  // Get current filter label for display
  $: currentFilterLabel = filterOptions.find(f => f.value === selectedFilter)?.label || 'Movies';

  onMount(() => {
    fetchMovies();
    fetchFavorites();
  });
</script>

<div class="movie-browser">
  <h2 class="text-2xl md:text-3xl font-bold mb-4 text-center">
    <span class="filter-label-display">{currentFilterLabel}</span>
  </h2>

  <!-- Filter Toggle Button - Hidden when panel is open -->
  {#if !showFilterPanel}
    <button class="filter-toggle" on:click={toggleFilterPanel} title="Toggle Filters">
      <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <line x1="3" y1="6" x2="21" y2="6"></line>
        <line x1="3" y1="12" x2="21" y2="12"></line>
        <line x1="3" y1="18" x2="21" y2="18"></line>
      </svg>
    </button>
  {/if}

  <!-- Filter Side Panel -->
  <div class="filter-panel {showFilterPanel ? 'open' : ''}">
    <div class="filter-header">
      <h3 class="filter-title">Browse By</h3>
      <button class="filter-close" on:click={toggleFilterPanel} title="Close Filters">
        <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M18 6 6 18"></path>
          <path d="m6 6 12 12"></path>
        </svg>
      </button>
    </div>
    <div class="filter-options">
      {#each filterOptions as filter}
        <button
          class="filter-option {selectedFilter === filter.value ? 'active' : ''}"
          on:click={() => selectFilter(filter.value)}
        >
          <span class="filter-icon">{filter.icon}</span>
          <span class="filter-label">{filter.label}</span>
        </button>
      {/each}
    </div>
  </div>

  <div class="main-content">
  <!-- Search Bar and Favorites Button -->
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
      <button type="button" on:click={openFavorites} class="favorites-button">
        <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"></path>
        </svg>
        Favorites {#if favorites.length > 0}({favorites.length}){/if}
      </button>
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
            <button
              class="favorite-icon absolute top-2 left-2 bg-black/70 text-white p-2 rounded-full hover:bg-black/90 transition-colors"
              on:click={() => toggleFavorite(movie)}
              title={favoritedMovieIds.has(movie.id) ? 'Remove from favorites' : 'Add to favorites'}
            >
              <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill={favoritedMovieIds.has(movie.id) ? 'red' : 'none'} stroke={favoritedMovieIds.has(movie.id) ? 'red' : 'currentColor'} stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"></path>
              </svg>
            </button>
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
</div>

<!-- Favorites Modal -->
{#if showFavorites}
  <div class="modal-overlay" on:click={closeFavorites}>
    <div class="modal-content favorites-modal" on:click|stopPropagation>
      <div class="modal-header">
        <h2 class="text-2xl font-bold text-white">My Favorites</h2>
        <button class="modal-close" on:click={closeFavorites} title="Close">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18 6 6 18"></path>
            <path d="m6 6 12 12"></path>
          </svg>
        </button>
      </div>
      <div class="favorites-content">
        {#if favorites.length === 0}
          <div class="no-favorites">
            <svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"></path>
            </svg>
            <p>No favorites yet</p>
            <p class="text-sm">Add movies to your favorites by clicking the heart icon</p>
          </div>
        {:else}
          <div class="movies-grid grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-4">
            {#each favorites as movie}
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
                  <button
                    class="favorite-icon absolute top-2 left-2 bg-black/70 text-white p-2 rounded-full hover:bg-black/90 transition-colors"
                    on:click={() => toggleFavorite(movie)}
                    title="Remove from favorites"
                  >
                    <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="red" stroke="red" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                      <path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"></path>
                    </svg>
                  </button>
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
                          on:click={() => {
                            closeFavorites();
                            playMovie(torrent);
                          }}
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
        {/if}
      </div>
    </div>
  </div>
{/if}

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
            <button class="loading-close" on:click={closeLoadingOverlay} title="Close loading overlay">
              <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M18 6 6 18"></path>
                <path d="m6 6 12 12"></path>
              </svg>
            </button>
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
    padding: 0;
    display: flex;
    flex-direction: column;
  }

  /* Filter Toggle Button */
  .filter-toggle {
    position: fixed;
    left: 1rem;
    top: 130px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 48px;
    height: 48px;
    padding: 0;
    background: hsl(var(--primary));
    color: hsl(var(--primary-foreground));
    border: none;
    border-radius: 0.5rem;
    cursor: pointer;
    transition: all 0.3s;
    z-index: 101;
    box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06);
  }

  .filter-toggle:hover {
    background: hsl(var(--primary) / 0.9);
    transform: scale(1.05);
    box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05);
  }

  .filter-toggle.hidden {
    opacity: 0;
    pointer-events: none;
  }

  /* Filter Panel Styles */
  .filter-panel {
    position: fixed;
    left: -320px;
    top: 0;
    width: 320px;
    height: 100vh;
    background: rgb(255, 255, 255);
    border-right: 1px solid hsl(var(--border));
    padding: 2rem 1.5rem;
    overflow-y: auto;
    z-index: 1000;
    transition: transform 0.25s ease-out;
    transform: translateX(0);
    box-shadow: 4px 0 24px rgba(0, 0, 0, 0.25);
  }

  :global(.dark) .filter-panel {
    background: rgb(23, 23, 23);
  }

  .filter-panel.open {
    transform: translateX(320px);
  }

  .filter-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 1rem;
  }

  .filter-title {
    font-size: 1rem;
    font-weight: 600;
    color: hsl(var(--foreground));
    margin: 0;
  }

  .filter-close {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    background: hsl(var(--muted));
    border: none;
    border-radius: 0.375rem;
    cursor: pointer;
    transition: all 0.2s;
    color: hsl(var(--foreground));
  }

  .filter-close:hover {
    background: hsl(var(--accent));
  }

  .filter-options {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .filter-option {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.75rem 1rem;
    border: none;
    border-radius: 0.5rem;
    background: hsl(var(--background));
    color: hsl(var(--foreground));
    font-size: 0.875rem;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.2s;
    text-align: left;
    width: 100%;
  }

  .filter-option:hover {
    background: hsl(var(--accent));
  }

  .filter-option.active {
    background: #3b82f6;
    color: white;
    font-weight: 700;
    border: 2px solid #2563eb;
  }

  .filter-icon {
    font-size: 1.25rem;
    line-height: 1;
  }

  .filter-label {
    flex: 1;
  }

  .main-content {
    width: 100%;
    padding: 0 2rem;
    max-width: 1200px;
    margin: 0 auto;
  }

  @media (max-width: 768px) {
    .filter-toggle {
      width: 44px;
      height: 44px;
      left: 0.5rem;
      top: 130px;
    }

    .filter-panel {
      left: -85%;
      width: 85%;
      padding: 1.5rem 1rem;
    }

    .filter-panel.open {
      transform: translateX(100%);
    }

    .filter-options {
      flex-direction: column;
      gap: 0.5rem;
    }

    .filter-option {
      width: 100%;
      padding: 0.75rem 1rem;
    }

    .main-content {
      width: 100%;
      padding: 0 1rem;
    }
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

  .favorites-button {
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
    background: hsl(var(--primary));
    color: hsl(var(--primary-foreground));
  }

  .favorites-button:hover {
    background: hsl(var(--primary) / 0.9);
  }

  .favorite-icon {
    cursor: pointer;
    z-index: 1;
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

  .loading-close {
    position: absolute;
    top: 1rem;
    right: 1rem;
    background-color: rgba(255, 255, 255, 0.1);
    border: 1px solid rgba(255, 255, 255, 0.3);
    border-radius: 50%;
    width: 40px;
    height: 40px;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    color: white;
    transition: all 0.2s;
    z-index: 11;
  }

  .loading-close:hover {
    background-color: rgba(255, 255, 255, 0.2);
    border-color: rgba(255, 255, 255, 0.5);
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

  /* Favorites Modal Styles */
  .favorites-modal {
    max-width: 1400px;
    max-height: 90vh;
    background-color: hsl(var(--background));
    display: flex;
    flex-direction: column;
  }

  .favorites-modal .modal-header {
    position: static;
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1.5rem;
    border-bottom: 1px solid hsl(var(--border));
    background-color: hsl(var(--card));
  }

  .favorites-modal .modal-header h2 {
    color: hsl(var(--foreground));
    margin: 0;
  }

  .favorites-content {
    flex: 1;
    overflow-y: auto;
    padding: 1.5rem;
  }

  .no-favorites {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 4rem 2rem;
    text-align: center;
    color: hsl(var(--muted-foreground));
  }

  .no-favorites svg {
    margin-bottom: 1rem;
    opacity: 0.5;
  }

  .no-favorites p {
    margin: 0.5rem 0;
    font-size: 1.125rem;
  }

  .no-favorites p.text-sm {
    font-size: 0.875rem;
    opacity: 0.7;
  }
</style>
