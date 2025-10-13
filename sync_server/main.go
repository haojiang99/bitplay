package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const (
	YTS_API_URL    = "https://yts.mx/api/v2/list_movies.json"
	SYNC_INTERVAL  = 5 * time.Minute
	MAX_PAGES      = 10 // Cache first 10 pages of movies
	DEFAULT_PORT   = 8080
)

// Cache structure to store YTS API responses
type MovieCache struct {
	sync.RWMutex
	data         map[string]interface{} // Stores full API responses by cache key
	lastSync     time.Time
}

var cache = &MovieCache{
	data: make(map[string]interface{}),
}

func init() {
	// Disable all log output
	log.SetOutput(io.Discard)
}

// Generate cache key from query parameters
func getCacheKey(page, limit int, query, sortBy, orderBy string) string {
	if query != "" {
		return fmt.Sprintf("search_%s_page_%d_limit_%d_sort_%s_order_%s", query, page, limit, sortBy, orderBy)
	}
	return fmt.Sprintf("page_%d_limit_%d_sort_%s_order_%s", page, limit, sortBy, orderBy)
}

// Fetch data from YTS.mx API
func fetchFromYTS(page, limit int, query, sortBy, orderBy string) (map[string]interface{}, error) {
	// Set defaults
	if sortBy == "" {
		sortBy = "date_added"
	}
	if orderBy == "" {
		orderBy = "desc"
	}

	apiURL := fmt.Sprintf("%s?page=%d&limit=%d&sort_by=%s&order_by=%s", YTS_API_URL, page, limit, sortBy, orderBy)

	if query != "" {
		apiURL = fmt.Sprintf("%s&query_term=%s", apiURL, url.QueryEscape(query))
	}

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from YTS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YTS API returned status: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode YTS response: %w", err)
	}

	// Add magnet URLs to torrents (same as main server)
	if data, ok := result["data"].(map[string]interface{}); ok {
		if movies, ok := data["movies"].([]interface{}); ok {
			for _, movieInterface := range movies {
				if movie, ok := movieInterface.(map[string]interface{}); ok {
					if title, ok := movie["title"].(string); ok {
						if torrents, ok := movie["torrents"].([]interface{}); ok {
							for _, torrentInterface := range torrents {
								if torrent, ok := torrentInterface.(map[string]interface{}); ok {
									if hash, ok := torrent["hash"].(string); ok {
										quality := ""
										if q, ok := torrent["quality"].(string); ok {
											quality = q
										}

										// Generate magnet link with trackers
										trackers := []string{
											"udp://open.demonii.com:1337/announce",
											"udp://tracker.openbittorrent.com:80",
											"udp://tracker.coppersurfer.tk:6969",
											"udp://glotorrents.pw:6969/announce",
											"udp://tracker.opentrackr.org:1337/announce",
											"udp://torrent.gresille.org:80/announce",
											"udp://p4p.arenabg.com:1337",
											"udp://tracker.leechers-paradise.org:6969",
										}

										magnetLink := fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s+%s",
											hash,
											url.QueryEscape(title),
											quality,
										)

										for _, tracker := range trackers {
											magnetLink += "&tr=" + url.QueryEscape(tracker)
										}

										torrent["magnetUrl"] = magnetLink
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return result, nil
}

// Sync popular pages to cache
func syncCache() {
	fmt.Printf("[%s] Starting cache sync...\n", time.Now().Format("15:04:05"))

	// Define popular sort combinations to cache
	sortCombinations := []struct {
		sortBy  string
		orderBy string
		name    string
	}{
		{"date_added", "desc", "Latest"},
		{"like_count", "desc", "Most Popular"},
		{"download_count", "desc", "Most Downloaded"},
		{"rating", "desc", "Top Rated"},
		{"seeds", "desc", "Best Availability"},
	}

	totalCached := 0
	// Sync first few pages for each sort combination
	for _, combo := range sortCombinations {
		for page := 1; page <= 3; page++ { // Cache 3 pages for each sort type
			cacheKey := getCacheKey(page, 20, "", combo.sortBy, combo.orderBy)

			data, err := fetchFromYTS(page, 20, "", combo.sortBy, combo.orderBy)
			if err != nil {
				fmt.Printf("[%s] Error syncing %s page %d: %v\n", time.Now().Format("15:04:05"), combo.name, page, err)
				continue
			}

			cache.Lock()
			cache.data[cacheKey] = data
			cache.Unlock()

			totalCached++
			// Small delay to avoid rate limiting
			time.Sleep(500 * time.Millisecond)
		}
	}

	cache.Lock()
	cache.lastSync = time.Now()
	cache.Unlock()

	fmt.Printf("[%s] Cache sync completed. Cached %d pages across %d sort types\n",
		time.Now().Format("15:04:05"), totalCached, len(sortCombinations))
}

// Start periodic sync
func startPeriodicSync() {
	// Initial sync
	syncCache()

	// Periodic sync
	ticker := time.NewTicker(SYNC_INTERVAL)
	go func() {
		for range ticker.C {
			syncCache()
		}
	}()
}

// API handler matching YTS.mx format
func handleListMovies(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	pageStr := r.URL.Query().Get("page")
	if pageStr == "" {
		pageStr = "1"
	}
	page, _ := strconv.Atoi(pageStr)

	limitStr := r.URL.Query().Get("limit")
	if limitStr == "" {
		limitStr = "20"
	}
	limit, _ := strconv.Atoi(limitStr)

	query := r.URL.Query().Get("query_term")
	sortBy := r.URL.Query().Get("sort_by")
	orderBy := r.URL.Query().Get("order_by")

	// Set defaults
	if sortBy == "" {
		sortBy = "date_added"
	}
	if orderBy == "" {
		orderBy = "desc"
	}

	cacheKey := getCacheKey(page, limit, query, sortBy, orderBy)

	// Try to get from cache first
	cache.RLock()
	cachedData, exists := cache.data[cacheKey]
	cache.RUnlock()

	var result map[string]interface{}

	if exists {
		// Return cached data
		result = cachedData.(map[string]interface{})
		fmt.Printf("[%s] ✓ Cache hit: page=%d sort=%s order=%s\n",
			time.Now().Format("15:04:05"), page, sortBy, orderBy)
	} else {
		// Fetch fresh data and cache it
		fmt.Printf("[%s] ✗ Cache miss, fetching: page=%d sort=%s order=%s query=%s\n",
			time.Now().Format("15:04:05"), page, sortBy, orderBy, query)

		data, err := fetchFromYTS(page, limit, query, sortBy, orderBy)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		// Cache the result
		cache.Lock()
		cache.data[cacheKey] = data
		cache.Unlock()

		result = data
	}

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(result)
}

// Health check endpoint
func handleHealth(w http.ResponseWriter, r *http.Request) {
	cache.RLock()
	lastSync := cache.lastSync
	cacheSize := len(cache.data)
	cache.RUnlock()

	response := map[string]interface{}{
		"status": "ok",
		"lastSync": lastSync.Format(time.RFC3339),
		"cacheSize": cacheSize,
		"syncInterval": SYNC_INTERVAL.String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func main() {
	// Start periodic sync in background
	startPeriodicSync()

	// Setup HTTP routes
	http.HandleFunc("/api/v2/list_movies.json", handleListMovies)
	http.HandleFunc("/health", handleHealth)

	port := DEFAULT_PORT
	addr := fmt.Sprintf("0.0.0.0:%d", port)

	fmt.Printf("\n------------------------------------------------\n")
	fmt.Printf("✅ YTS Sync Server started!\n")
	fmt.Printf("   Local:    http://localhost:%d/api/v2/list_movies.json\n", port)
	fmt.Printf("   External: http://66.42.87.30:%d/api/v2/list_movies.json\n", port)
	fmt.Printf("   Sync interval: %s\n", SYNC_INTERVAL)
	fmt.Printf("   Health check: http://66.42.87.30:%d/health\n", port)
	fmt.Printf("------------------------------------------------\n\n")

	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}
