package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"net/url"
	"path/filepath"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/net/proxy"

	"database/sql"
	_ "modernc.org/sqlite"
)

func init() {
	// Enable log output for debugging
	log.SetOutput(os.Stdout)
}

var (
	currentSettings Settings
	settingsMutex   sync.RWMutex
	db              *sql.DB
)

type TorrentSession struct {
	Client      *torrent.Client
	Torrent     *torrent.Torrent
	Port        int
	LastUsed    time.Time
	TempDataDir string // Track temp directory for cleanup
}

type Settings struct {
	EnableProxy    bool   `json:"enableProxy"`
	ProxyURL       string `json:"proxyUrl"`
	EnableProwlarr bool   `json:"enableProwlarr"`
	ProwlarrHost   string `json:"prowlarrHost"`
	ProwlarrApiKey string `json:"prowlarrApiKey"`
	EnableJackett  bool   `json:"enableJackett"`
	JackettHost    string `json:"jackettHost"`
	JackettApiKey  string `json:"jackettApiKey"`
	YTSServerURL   string `json:"ytsServerUrl"` // YTS API server URL
}

type ProxySettings struct {
	EnableProxy bool   `json:"enableProxy"`
	ProxyURL    string `json:"proxyUrl"`
}

type ProwlarrSettings struct {
	EnableProwlarr bool   `json:"enableProwlarr"`
	ProwlarrHost   string `json:"prowlarrHost"`
	ProwlarrApiKey string `json:"prowlarrApiKey"`
}

type JackettSettings struct {
	EnableJackett bool   `json:"enableJackett"`
	JackettHost   string `json:"jackettHost"`
	JackettApiKey string `json:"jackettApiKey"`
}

type YTSSettings struct {
	YTSServerURL string `json:"ytsServerUrl"`
}

var (
	sessions  sync.Map
	usedPorts sync.Map
	portMutex sync.Mutex
)

// Helper function to format file sizes
func formatSize(sizeInBytes float64) string {
	if sizeInBytes < 1024 {
		return fmt.Sprintf("%.0f B", sizeInBytes)
	}

	sizeInKB := sizeInBytes / 1024
	if sizeInKB < 1024 {
		return fmt.Sprintf("%.2f KB", sizeInKB)
	}

	sizeInMB := sizeInKB / 1024
	if sizeInMB < 1024 {
		return fmt.Sprintf("%.2f MB", sizeInMB)
	}

	sizeInGB := sizeInMB / 1024
	return fmt.Sprintf("%.2f GB", sizeInGB)
}

var (
	proxyTransport = &http.Transport{
		// copy your existing timeouts & DialContext logic here...
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConnsPerHost:   10,
	}
	proxyClient = &http.Client{
		Transport: proxyTransport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			for k, vv := range via[0].Header {
				if _, ok := req.Header[k]; !ok {
					req.Header[k] = vv
				}
			}
			return nil
		},
	}
)

func createSelectiveProxyClient() *http.Client {
	settingsMutex.RLock()
	defer settingsMutex.RUnlock()

	if !currentSettings.EnableProxy {
		return &http.Client{Timeout: 30 * time.Second}
	}
	// Reconfigure proxyTransport’s DialContext if URL changed:
	dialer, _ := createProxyDialer(currentSettings.ProxyURL)
	proxyTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialer.Dial(network, addr)
	}
	// Drop any old idle conns after reconfiguration:
	proxyTransport.CloseIdleConnections()

	return proxyClient
}

// Create a proxy dialer for SOCKS5
func createProxyDialer(proxyURL string) (proxy.Dialer, error) {
	proxyURLParsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse proxy URL: %v", err)
	}

	// Extract auth information
	auth := &proxy.Auth{}
	if proxyURLParsed.User != nil {
		auth.User = proxyURLParsed.User.Username()
		if password, ok := proxyURLParsed.User.Password(); ok {
			auth.Password = password
		}
	}

	// Create a SOCKS5 dialer
	return proxy.SOCKS5("tcp", proxyURLParsed.Host, auth, proxy.Direct)
}

// Implement a port allocation function to prevent conflicts
func getAvailablePort() int {
	portMutex.Lock()
	defer portMutex.Unlock()

	// Try up to 50 times to find an unused port
	for i := 0; i < 50; i++ {
		// Generate a random port in the high range
		port := 10000 + rand.Intn(50000)

		// Check if this port is already in use by our app
		if _, exists := usedPorts.Load(port); !exists {
			// Mark this port as used
			usedPorts.Store(port, true)
			return port
		}
	}

	// If we can't find an available port, return a very high random port
	// as a last resort
	return 60000 + rand.Intn(5000)
}

// Release a port when we're done with it
func releasePort(port int) {
	portMutex.Lock()
	defer portMutex.Unlock()
	usedPorts.Delete(port)
}

// Initialize the torrent client with proxy settings
// Returns: client, port, tempDir, error
func initTorrentWithProxy() (*torrent.Client, int, string, error) {
	settingsMutex.RLock()
	enableProxy := currentSettings.EnableProxy
	proxyURL := currentSettings.ProxyURL
	settingsMutex.RUnlock()

	config := torrent.NewDefaultClientConfig()

	// Create unique temp directory for this session in OS temp location
	// This will be automatically cleaned up by OS or our cleanup routine
	tempDir, err := os.MkdirTemp("", "bitplay-torrent-*")
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Use temp directory for storage - will be deleted when session ends
	config.DefaultStorage = storage.NewFile(tempDir)
	port := getAvailablePort()
	config.ListenPort = port

	// Disable uploading/seeding
	config.NoUpload = true
	config.Seed = false
	config.DisableTrackers = false // Keep trackers for getting peers
	config.DisablePEX = true        // Disable peer exchange
	config.DisableIPv6 = false

	// Set upload rate to 0 to prevent any uploading
	config.UploadRateLimiter = nil

	if enableProxy {
		os.Setenv("ALL_PROXY", proxyURL)
		os.Setenv("SOCKS_PROXY", proxyURL)
		os.Setenv("HTTP_PROXY", proxyURL)
		os.Setenv("HTTPS_PROXY", proxyURL)

		proxyDialer, err := createProxyDialer(proxyURL)
		if err != nil {
			releasePort(port)
			os.RemoveAll(tempDir)
			return nil, port, "", fmt.Errorf("could not create proxy dialer: %v", err)
		}

		config.HTTPProxy = func(*http.Request) (*url.URL, error) {
			return url.Parse(proxyURL)
		}

		client, err := torrent.NewClient(config)
		if err != nil {
			releasePort(port)
			os.RemoveAll(tempDir) // Clean up temp dir on error
			return nil, port, "", err
		}

		setValue(client, "dialerNetwork", func(ctx context.Context, network, addr string) (net.Conn, error) {
			return proxyDialer.Dial(network, addr)
		})

		return client, port, tempDir, nil
	}

	os.Unsetenv("ALL_PROXY")
	os.Unsetenv("SOCKS_PROXY")
	os.Unsetenv("HTTP_PROXY")
	os.Unsetenv("HTTPS_PROXY")

	client, err := torrent.NewClient(config)
	if err != nil {
		releasePort(port)
		os.RemoveAll(tempDir) // Clean up temp dir on error
		return nil, port, "", err
	}
	return client, port, tempDir, nil
}

// Helper function to try to set a field value using reflection
// This is a bit hacky but might help override the client's dialer
func setValue(obj interface{}, fieldName string, value interface{}) {
	// This is a best-effort approach that may not work with all library versions
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Warning: Could not set %s field: %v", fieldName, r)
		}
	}()

	reflectValue := reflect.ValueOf(obj).Elem()
	field := reflectValue.FieldByName(fieldName)

	if field.IsValid() && field.CanSet() {
		field.Set(reflect.ValueOf(value))
		log.Printf("Successfully set %s to use proxy", fieldName)
	}
}

// Override system settings with our proxy
func init() {

	// check if settings.json exists
	if _, err := os.Stat("config/settings.json"); os.IsNotExist(err) {
		log.Println("settings.json not found, creating default settings")
		defaultSettings := Settings{
			EnableProxy:    false,
			ProxyURL:       "",
			EnableProwlarr: false,
			ProwlarrHost:   "",
			ProwlarrApiKey: "",
			EnableJackett:  false,
			JackettHost:    "",
			JackettApiKey:  "",
			YTSServerURL:   "https://yts.mx/api/v2/list_movies.json", // Default to YTS.mx
		}
		// Create the config directory if it doesn't exist
		if err := os.MkdirAll("config", 0755); err != nil {
			log.Fatalf("Failed to create config directory: %v", err)
		}
		settingsFile, err := os.Create("config/settings.json")
		if err != nil {
			log.Fatalf("Failed to create settings.json: %v", err)
		}
		defer settingsFile.Close()
		encoder := json.NewEncoder(settingsFile)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(defaultSettings); err != nil {
			log.Fatalf("Failed to encode default settings: %v", err)
		}
		log.Println("Default settings created in settings.json")
	}

	// Load settings from settings.json
	settingsFile, err := os.Open("config/settings.json")
	if err != nil {
		log.Fatalf("Failed to open settings.json: %v", err)
	}
	defer settingsFile.Close()

	var s Settings
	if err := json.NewDecoder(settingsFile).Decode(&s); err != nil {
		log.Fatalf("Failed to decode settings.json: %v", err)
	}

	// Set default YTS server URL if not set
	if s.YTSServerURL == "" {
		s.YTSServerURL = "https://yts.mx/api/v2/list_movies.json"
	}

	settingsMutex.Lock()
	currentSettings = s
	settingsMutex.Unlock()
}

// Initialize SQLite database for favorites
func initDatabase() error {
	// Create database in config directory
	dbPath := filepath.Join("config", "favorites.db")

	var err error
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(1) // SQLite works best with single connection
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	// Enable WAL mode for better concurrency
	_, err = db.Exec("PRAGMA journal_mode=WAL;")
	if err != nil {
		return fmt.Errorf("failed to set WAL mode: %w", err)
	}

	// Create favorites table
	createTableSQL := `CREATE TABLE IF NOT EXISTS favorites (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		movie_id INTEGER NOT NULL UNIQUE,
		title TEXT NOT NULL,
		year INTEGER,
		rating REAL,
		runtime INTEGER,
		genres TEXT,
		summary TEXT,
		cover_image TEXT,
		torrents TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	_, err = db.Exec(createTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create table: %w", err)
	}

	return nil
}

// Clean up old temp directories from previous runs
func cleanupOldTempDirs() {
	tempDir := os.TempDir()
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		// Look for our temp directories (bitplay-torrent-*)
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "bitplay-torrent-") {
			fullPath := filepath.Join(tempDir, entry.Name())
			os.RemoveAll(fullPath)
		}
	}
}

func main() {
	// Seed random number generator
	rand.Seed(time.Now().UnixNano())

	// Initialize favorites database
	if err := initDatabase(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Clean up any leftover temp directories from previous runs
	cleanupOldTempDirs()

	// Force proxy for all Go HTTP connections
	setGlobalProxy()

	// Set up endpoint handlers
	http.HandleFunc("/api/v1/torrent/add", addTorrentHandler)
	http.HandleFunc("/api/v1/torrent/", torrentHandler)
	http.HandleFunc("/api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			settingsMutex.RLock()
			defer settingsMutex.RUnlock()
			respondWithJSON(w, http.StatusOK, currentSettings)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("/api/v1/settings/proxy", saveProxySettingsHandler)
	http.HandleFunc("/api/v1/settings/prowlarr", saveProwlarrSettingsHandler)
	http.HandleFunc("/api/v1/settings/jackett", saveJackettSettingsHandler)
	http.HandleFunc("/api/v1/settings/yts", saveYTSSettingsHandler)
	http.HandleFunc("/api/v1/prowlarr/search", searchFromProwlarr)
	http.HandleFunc("/api/v1/jackett/search", searchFromJackett)
	http.HandleFunc("/api/v1/prowlarr/test", testProwlarrConnection)
	http.HandleFunc("/api/v1/jackett/test", testJackettConnection)
	http.HandleFunc("/api/v1/proxy/test", testProxyConnection)
	http.HandleFunc("/api/v1/torrent/convert", convertTorrentToMagnetHandler)
	http.HandleFunc("/api/v1/yts/movies", fetchYTSMovies)
	http.HandleFunc("/api/v1/avmoo/movies", fetchAvmooMovies)
	http.HandleFunc("/api/v1/avmoo/movie/", fetchAvmooMovieDetail)

	// Favorites endpoints
	http.HandleFunc("/api/v1/favorites", favoritesHandler)
	http.HandleFunc("/api/v1/favorites/add", addFavoriteHandler)
	http.HandleFunc("/api/v1/favorites/remove/", removeFavoriteHandler)

	// Set up client file serving
	http.Handle("/", http.FileServer(http.Dir("./client")))
	http.HandleFunc("/client/", func(w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/client/", http.FileServer(http.Dir("./client"))).ServeHTTP(w, r)
	})
	http.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./client/favicon.ico")
	})

	go cleanupSessions()

	port := 3147

	addr := fmt.Sprintf("0.0.0.0:%d", port)

	// Create channel to signal if server started successfully
	serverStarted := make(chan bool, 1)

	// Create a server with graceful shutdown
	server := &http.Server{
		Addr:    addr,
		Handler: nil, // Use the default ServeMux
	}

	// Start the server in a goroutine
	go func() {
		err := server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			serverStarted <- false
		}
	}()

	// Give the server a moment to start or fail
	select {
	case success := <-serverStarted:
		if !success {
			return
		}
	case <-time.After(1 * time.Second):
		// No immediate error, assume it started successfully
		fmt.Printf("\n------------------------------------------------\n")
		fmt.Printf("✅ Server started! Open in your browser:\n")
		fmt.Printf("   http://localhost:%d\n", port)
		fmt.Printf("------------------------------------------------\n\n")

		// Block forever (the server is running in a goroutine)
		select {}
	}
}

// Set up global proxy for all Go HTTP calls
func setGlobalProxy() {
	settingsMutex.RLock()
	enableProxy := currentSettings.EnableProxy
	proxyURL := currentSettings.ProxyURL
	settingsMutex.RUnlock()

	if !enableProxy {
		log.Println("Proxy is disabled, not setting global HTTP proxy.")
		return
	}

	proxyDialer, err := createProxyDialer(proxyURL)
	if err != nil {
		log.Printf("Warning: Could not create proxy dialer: %v", err)
		return
	}

	httpTransport, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		httpTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return proxyDialer.Dial(network, addr)
		}
		log.Printf("Successfully configured SOCKS5 proxy for all HTTP traffic: %s", proxyURL)
	} else {
		log.Println("⚠️ Warning: Could not override HTTP transport")
	}
}

// Handler to add a torrent using a magnet link
func addTorrentHandler(w http.ResponseWriter, r *http.Request) {
	var request struct{ Magnet string }
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}

	magnet := request.Magnet
	if magnet == "" {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "No magnet link provided"})
	}

	// handle http links like Prowlarr or Jackett
	if strings.HasPrefix(request.Magnet, "http") {
		// Use the client that bypasses proxy for Prowlarr
		httpClient := createSelectiveProxyClient()

		httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}

		// Make the HTTP request to follow the Prowlarr link
		req, err := http.NewRequest("GET", request.Magnet, nil)
		if err != nil {
			log.Printf("Error creating request: %v", err)
			respondWithJSON(w, http.StatusBadRequest, map[string]string{
				"error": "Invalid URL: " + err.Error(),
			})
			return
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

		// Follow the Prowlarr link
		log.Printf("Following Prowlarr URL: %s", request.Magnet)
		resp, err := httpClient.Do(req)
		if err != nil {
			log.Printf("Error following URL: %v", err)
			respondWithJSON(w, http.StatusBadRequest, map[string]string{
				"error": "Failed to download: " + err.Error(),
			})
			return
		}
		defer resp.Body.Close()

		log.Printf("Got response: %d %s", resp.StatusCode, resp.Status)

		// Check for redirects to magnet links
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			log.Printf("Found redirect to: %s", location)

			if strings.HasPrefix(location, "magnet:") {
				log.Printf("Found magnet redirect: %s", location)
				magnet = location
			} else {
				log.Printf("Non-magnet redirect: %s", location)
				respondWithJSON(w, http.StatusBadRequest, map[string]string{
					"error": "URL redirects to non-magnet content",
				})
				return
			}
		}
	}

	// check if magnet link is valid
	if magnet == "" || !strings.HasPrefix(magnet, "magnet:") {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid magnet link"})
		return
	}

	// Use the simpler, more secure proxy configuration
	client, port, tempDir, err := initTorrentWithProxy()
	if err != nil {
		log.Printf("Client creation error: %v", err)
		respondWithJSON(w, http.StatusInternalServerError,
			map[string]string{"error": "Failed to create client with proxy"})
		return
	}

	// if we bail out before session‑storage, make sure to release resources
	defer func() {
		if client != nil {
			releasePort(port)
			client.Close()
			// Clean up temp directory if session not created
			if tempDir != "" {
				os.RemoveAll(tempDir)
			}
		}
	}()

	t, err := client.AddMagnet(magnet)
	if err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid magnet url"})
		return
	}
	select {
	case <-t.GotInfo():
	case <-time.After(3 * time.Minute):
		respondWithJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "Timeout getting info - proxy might be blocking BitTorrent traffic"})
	}

	sessionID := t.InfoHash().HexString()
	sessions.Store(sessionID, &TorrentSession{
		Client:      client,
		Torrent:     t,
		Port:        port,
		LastUsed:    time.Now(),
		TempDataDir: tempDir, // Store temp dir for cleanup
	})

	// Set client to nil so it doesn't get closed by the defer function
	// since it's now stored in the sessions map
	client = nil

	respondWithJSON(w, http.StatusOK, map[string]string{"sessionId": sessionID})
}

// Torrent handler to serve torrent files and stream content
func torrentHandler(w http.ResponseWriter, r *http.Request) {
	// Extract sessionId and possibly fileIndex from the URL
	parts := strings.Split(r.URL.Path, "/")

	// The URL structure is /api/v1/torrent/[sessionId]/...
	if len(parts) < 5 {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid path"})
		return
	}

	// The session ID is at position 4
	sessionID := parts[4]

	// Get the torrent session from our sessions map
	sessionValue, ok := sessions.Load(sessionID)
	if !ok {
		respondWithJSON(w, http.StatusNotFound, map[string]string{
			"error": "Session not found",
			"id":    sessionID,
		})
		return
	}
	session := sessionValue.(*TorrentSession)
	session.LastUsed = time.Now() // Update last used time

	// If there's a streaming request, handle it
	if len(parts) > 5 && parts[5] == "stream" { // Changed from parts[4] to parts[5]
		if len(parts) < 7 { // Changed from 6 to 7
			http.Error(w, "Invalid stream path", http.StatusBadRequest)
			return
		}

		fileIndexString := parts[6]
		// remove .vtt from fileIndex if it exists
		fileIndexString = strings.TrimSuffix(fileIndexString, ".vtt")

		fileIndex, err := strconv.Atoi(fileIndexString)

		if err != nil {
			http.Error(w, "Invalid file index", http.StatusBadRequest)
			return
		}

		if fileIndex < 0 || fileIndex >= len(session.Torrent.Files()) {
			http.Error(w, "File index out of range", http.StatusBadRequest)
			return
		}

		file := session.Torrent.Files()[fileIndex]

		// Set appropriate Content-Type based on file extension
		fileName := file.DisplayPath()
		extension := strings.ToLower(filepath.Ext(fileName))

		switch extension {
		case ".mp4":
			w.Header().Set("Content-Type", "video/mp4")
		case ".webm":
			w.Header().Set("Content-Type", "video/webm")
		case ".mkv":
			w.Header().Set("Content-Type", "video/x-matroska")
		case ".avi":
			w.Header().Set("Content-Type", "video/x-msvideo")
		case ".srt":
			// For SRT, convert to VTT on-the-fly if requested as VTT
			if r.URL.Query().Get("format") == "vtt" {
				w.Header().Set("Content-Type", "text/vtt")
				w.Header().Set("Access-Control-Allow-Origin", "*") // Allow cross-origin requests

				// Read the SRT file with size limit
				reader := file.NewReader()
				// Wrap with limiting reader to prevent memory issues (10MB max)
				limitReader := io.LimitReader(reader, 10*1024*1024) // 10MB limit for subtitles
				srtBytes, err := io.ReadAll(limitReader)
				if err != nil {
					http.Error(w, "Failed to read subtitle file", http.StatusInternalServerError)
					return
				}

				// Convert from SRT to VTT
				vttBytes := convertSRTtoVTT(srtBytes)
				w.Write(vttBytes)
				return
			} else {
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Access-Control-Allow-Origin", "*") // Allow cross-origin requests
			}
		case ".vtt":
			w.Header().Set("Content-Type", "text/vtt")
			w.Header().Set("Access-Control-Allow-Origin", "*") // Allow cross-origin requests
		case ".sub":
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Access-Control-Allow-Origin", "*") // Allow cross-origin requests
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
		}

		// Add CORS headers for all content
		// Stream the file
		reader := file.NewReader()
		// ServeContent will close the reader when done but we need to
		// ensure it gets closed if there's a panic or other error
		defer func() {
			if closer, ok := reader.(io.Closer); ok {
				closer.Close()
				println("Closed reader***************************************")
			}
		}()
		println("Serving content*****************************************")
		http.ServeContent(w, r, fileName, time.Time{}, reader)
		return
	}

	// If we get here, just return file list
	var files []map[string]interface{}
	for i, file := range session.Torrent.Files() {
		files = append(files, map[string]interface{}{
			"index": i,
			"name":  file.DisplayPath(),
			"size":  file.Length(),
		})
	}

	respondWithJSON(w, http.StatusOK, files)
}

// Add a function to convert SRT to VTT format
func convertSRTtoVTT(srtBytes []byte) []byte {
	srtContent := string(srtBytes)

	// Add VTT header
	vttContent := "WEBVTT\n\n"

	// Convert SRT content to VTT format
	// Simple conversion - replace timestamps format
	lines := strings.Split(srtContent, "\n")

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Skip subtitle numbers
		if _, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			continue
		}

		// Convert timestamp lines
		if strings.Contains(line, " --> ") {
			// SRT: 00:00:20,000 --> 00:00:24,400
			// VTT: 00:00:20.000 --> 00:00:24.400
			line = strings.Replace(line, ",", ".", -1)
			vttContent += line + "\n"
		} else {
			vttContent += line + "\n"
		}
	}

	return []byte(vttContent)
}

// Helper function to respond with JSON
func respondWithJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// Update cleanupSessions with temp directory cleanup
func cleanupSessions() {
	ticker := time.NewTicker(2 * time.Minute) // Check more frequently
	defer ticker.Stop()

	for range ticker.C {
		cleaned := 0
		sessions.Range(func(key, value interface{}) bool {
			session := value.(*TorrentSession)

			// Clean up sessions inactive for more than 10 minutes
			if time.Since(session.LastUsed) > 10*time.Minute {
				// Drop torrent first
				session.Torrent.Drop()
				// Close client
				session.Client.Close()
				// Release port
				releasePort(session.Port)
				// Remove temp directory
				if session.TempDataDir != "" {
					os.RemoveAll(session.TempDataDir)
				}
				// Remove from map
				sessions.Delete(key)
				cleaned++
			}
			return true
		})

		if cleaned > 0 {
			// Force garbage collection to free memory
			runtime.GC()
		}
	}
}

// Test the proxy connection
func testProwlarrConnection(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		return
	}

	var settings ProwlarrSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	prowlarrHost := settings.ProwlarrHost
	prowlarrApiKey := settings.ProwlarrApiKey

	if prowlarrHost == "" || prowlarrApiKey == "" {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Prowlarr host or API key not set"})
		return
	}

	client := createSelectiveProxyClient()
	testURL := fmt.Sprintf("%s/api/v1/system/status", prowlarrHost)

	req, err := http.NewRequest("GET", testURL, nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	req.Header.Set("X-Api-Key", prowlarrApiKey)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error making request to Prowlarr: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to connect to Prowlarr: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respondWithJSON(w, resp.StatusCode, map[string]string{"error": fmt.Sprintf("Prowlarr returned status %d", resp.StatusCode)})
		return
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read Prowlarr response"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(responseBody)
}

// Search from Prowlarr
func searchFromProwlarr(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Prowlarr-Host, X-Api-Key")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "No search query provided"})
		return
	}

	// search movies in prowlarr
	settingsMutex.RLock()
	prowlarrHost := currentSettings.ProwlarrHost
	prowlarrApiKey := currentSettings.ProwlarrApiKey
	settingsMutex.RUnlock()

	if prowlarrHost == "" || prowlarrApiKey == "" {
		http.Error(w, "Prowlarr host or API key not set", http.StatusBadRequest)
		return
	}

	// Use the client that bypasses proxy for Prowlarr
	client := createSelectiveProxyClient()

	// Prowlarr search endpoint - looking for movie torrents
	searchURL := fmt.Sprintf("%s/api/v1/search?query=%s&limit=10", prowlarrHost, url.QueryEscape(query))

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	req.Header.Set("X-Api-Key", prowlarrApiKey)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error making request to Prowlarr: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to connect to Prowlarr: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read Prowlarr response"})
		return
	}

	if resp.StatusCode != http.StatusOK {
		respondWithJSON(w, resp.StatusCode, map[string]string{"error": fmt.Sprintf("Prowlarr returned status %d: %s", resp.StatusCode, string(body))})
		return
	}

	// Parse the JSON response and process the results
	var results []map[string]interface{}
	if err := json.Unmarshal(body, &results); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to parse Prowlarr response"})
		return
	}

	// Process the results to make them more usable by the frontend
	var processedResults []map[string]interface{}
	for _, result := range results {
		// Get title and download URL
		title, hasTitle := result["title"].(string)
		downloadUrl, hasDownloadUrl := result["downloadUrl"].(string)

		// Magnet URL might be present in some results
		magnetUrl, hasMagnet := result["magnetUrl"].(string)

		if !hasTitle || title == "" {
			// Skip results without titles
			continue
		}

		// We need at least one of download URL or magnet URL
		if (!hasDownloadUrl || downloadUrl == "") && (!hasMagnet || magnetUrl == "") {
			continue
		}

		// Create a simplified result object with just what we need
		processedResult := map[string]interface{}{
			"title": title,
		}

		// Prefer magnet URLs if available directly
		if hasMagnet && magnetUrl != "" {
			processedResult["magnetUrl"] = magnetUrl
			processedResult["directMagnet"] = true
		} else if hasDownloadUrl && downloadUrl != "" {
			processedResult["downloadUrl"] = downloadUrl
			processedResult["directMagnet"] = false
		}

		// Include optional fields if they exist
		if size, ok := result["size"].(float64); ok {
			processedResult["size"] = formatSize(size)
		}

		if seeders, ok := result["seeders"].(float64); ok {
			processedResult["seeders"] = seeders
		}

		if leechers, ok := result["leechers"].(float64); ok {
			processedResult["leechers"] = leechers
		}

		if indexer, ok := result["indexer"].(string); ok {
			processedResult["indexer"] = indexer
		}

		if publishDate, ok := result["publishDate"].(string); ok {
			processedResult["publishDate"] = publishDate
		}

		if category, ok := result["category"].(string); ok {
			processedResult["category"] = category
		}

		processedResults = append(processedResults, processedResult)
	}

	respondWithJSON(w, http.StatusOK, processedResults)
}

// Test Jackett Connection Handler
func testJackettConnection(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	// Handle preflight requests
	if r.Method == "OPTIONS" {
		return
	}

	var settings JackettSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	jackettHost := settings.JackettHost
	jackettApiKey := settings.JackettApiKey

	if jackettHost == "" || jackettApiKey == "" {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Jackett host or API key not set"})
		return
	}

	client := createSelectiveProxyClient()
	testURL := fmt.Sprintf("%s/api/v2.0/indexers/all/results?apikey=%s", jackettHost, jackettApiKey)
	req, err := http.NewRequest("GET", testURL, nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error making request to Jackett: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to connect to Jackett: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respondWithJSON(w, resp.StatusCode, map[string]string{"error": fmt.Sprintf("Jackett returned status %d", resp.StatusCode)})
		return
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read Jackett response"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(responseBody)
}

// Search from Jackett
func searchFromJackett(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "No search query provided"})
		return
	}

	// search movies in jackett
	settingsMutex.RLock()
	jackettHost := currentSettings.JackettHost
	jackettApiKey := currentSettings.JackettApiKey
	settingsMutex.RUnlock()

	if jackettHost == "" || jackettApiKey == "" {
		http.Error(w, "Jackett host or API key not set", http.StatusBadRequest)
		return
	}

	// Use the client that bypasses proxy for Jackett
	client := createSelectiveProxyClient()

	// Jackett search endpoint - looking for movie torrents
	searchURL := fmt.Sprintf("%s/api/v2.0/indexers/all/results?Query=%s&apikey=%s", jackettHost, url.QueryEscape(query), jackettApiKey)

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error making request to Jackett: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to connect to Jackett: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read Jackett response"})
		return
	}

	if resp.StatusCode != http.StatusOK {
		respondWithJSON(w, resp.StatusCode, map[string]string{"error": fmt.Sprintf("Jackett returned status %d: %s", resp.StatusCode, string(body))})
		return
	}

	var jacketResponse struct {
		Results []map[string]interface{} `json:"Results"`
	}

	// Parse the JSON response and process the results
	if err := json.Unmarshal(body, &jacketResponse); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to parse Jackett response"})
		return
	}

	// Process the results to make them more usable by the frontend
	var processedResults []map[string]interface{}
	for _, result := range jacketResponse.Results {
		// Get title and download URL
		title, hasTitle := result["Title"].(string)
		downloadUrl, hasDownloadUrl := result["Link"].(string)

		// Magnet URL might be present in some results
		magnetUrl, hasMagnet := result["MagnetUri"].(string)

		if !hasTitle || title == "" {
			// Skip results without titles
			continue
		}

		// We need at least one of download URL or magnet URL
		if (!hasDownloadUrl || downloadUrl == "") && (!hasMagnet || magnetUrl == "") {
			continue
		}

		// Create a simplified result object with just what we need
		processedResult := map[string]interface{}{
			"title": title,
		}

		// Prefer magnet URLs if available directly
		if hasMagnet && magnetUrl != "" && strings.HasPrefix(magnetUrl, "magnet:") {
			processedResult["magnetUrl"] = magnetUrl
			processedResult["directMagnet"] = true
		} else if hasDownloadUrl && downloadUrl != "" {
			processedResult["downloadUrl"] = downloadUrl
			processedResult["directMagnet"] = false
		}

		// Include optional fields if they exist
		if size, ok := result["Size"].(float64); ok {
			processedResult["size"] = formatSize(size)
		}

		if seeders, ok := result["Seeders"].(float64); ok {
			processedResult["seeders"] = seeders
		}

		if leechers, ok := result["Peers"].(float64); ok {
			processedResult["leechers"] = leechers
		}

		if indexer, ok := result["Tracker"].(string); ok {
			processedResult["indexer"] = indexer
		}

		if publishDate, ok := result["PublishDate"].(string); ok {
			processedResult["publishDate"] = publishDate
		}

		if category, ok := result["category"].(string); ok {
			processedResult["category"] = category
		}

		processedResults = append(processedResults, processedResult)
	}

	respondWithJSON(w, http.StatusOK, processedResults)
}

// Test Proxy Connection Handler
func testProxyConnection(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var settings ProxySettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	proxyURL := settings.ProxyURL

	if proxyURL == "" {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Proxy URL not set"})
		return
	}

	// Parse the proxy URL
	parsedProxyURL, err := url.Parse(proxyURL)
	if err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid proxy URL: " + err.Error()})
		return
	}

	// Create a transport that uses the proxy
	transport := &http.Transport{
		Proxy: http.ProxyURL(parsedProxyURL),
	}

	// Create client with custom transport and timeout
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second, // Adjust timeout as needed
	}

	testURL := "https://httpbin.org/ip"
	req, err := http.NewRequest("GET", testURL, nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error making request through proxy: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Proxy connection failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read proxy response"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(responseBody)
}

// Helper function to save settings to file (assumes mutex is already locked)
func saveSettingsToFile() error {
	// Create the directory if it doesn't exist
	if err := os.MkdirAll("config", 0755); err != nil {
		log.Fatalf("Failed to create config directory: %v", err)
	}

	file, err := os.Create("config/settings.json")
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(currentSettings); err != nil {
		return err
	}

	return nil
}

// Proxy Settings Save Handler
func saveProxySettingsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var newSettings ProxySettings
	if err := json.NewDecoder(r.Body).Decode(&newSettings); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	settingsMutex.RLock()
	currentSettings.EnableProxy = newSettings.EnableProxy
	currentSettings.ProxyURL = newSettings.ProxyURL
	defer settingsMutex.RUnlock()

	if err := saveSettingsToFile(); err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save settings: " + err.Error()})
		return
	}
	println("Proxy settings saved successfully")

	setGlobalProxy()

	respondWithJSON(w, http.StatusOK, map[string]string{"message": "Proxy settings saved successfully"})
}

// Prowlarr Settings Save Handler
func saveProwlarrSettingsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var newSettings ProwlarrSettings
	if err := json.NewDecoder(r.Body).Decode(&newSettings); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	settingsMutex.RLock()
	currentSettings.EnableProwlarr = newSettings.EnableProwlarr
	currentSettings.ProwlarrHost = newSettings.ProwlarrHost
	currentSettings.ProwlarrApiKey = newSettings.ProwlarrApiKey
	defer settingsMutex.RUnlock()

	if err := saveSettingsToFile(); err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save settings: " + err.Error()})
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"message": "Prowlarr settings saved successfully"})
}

// Jackett Settings Save Handler
func saveJackettSettingsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var newSettings JackettSettings
	if err := json.NewDecoder(r.Body).Decode(&newSettings); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	settingsMutex.RLock()
	currentSettings.EnableJackett = newSettings.EnableJackett
	currentSettings.JackettHost = newSettings.JackettHost
	currentSettings.JackettApiKey = newSettings.JackettApiKey
	defer settingsMutex.RUnlock()

	if err := saveSettingsToFile(); err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save settings: " + err.Error()})
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"message": "Jackett settings saved successfully"})
}

// YTS Settings Save Handler
func saveYTSSettingsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var newSettings YTSSettings
	if err := json.NewDecoder(r.Body).Decode(&newSettings); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	settingsMutex.RLock()
	currentSettings.YTSServerURL = newSettings.YTSServerURL
	defer settingsMutex.RUnlock()

	if err := saveSettingsToFile(); err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save settings: " + err.Error()})
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"message": "YTS server settings saved successfully"})
}

// Favorites Handlers
func favoritesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rows, err := db.Query(`SELECT movie_id, title, year, rating, runtime, genres, summary, cover_image, torrents, created_at
		FROM favorites ORDER BY created_at DESC`)
	if err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch favorites"})
		return
	}
	defer rows.Close()

	var favorites []map[string]interface{}
	for rows.Next() {
		var movieID int
		var title, genres, summary, coverImage, torrents, createdAt string
		var year, runtime int
		var rating float64

		err := rows.Scan(&movieID, &title, &year, &rating, &runtime, &genres, &summary, &coverImage, &torrents, &createdAt)
		if err != nil {
			continue
		}

		// Parse torrents JSON
		var torrentsData []interface{}
		json.Unmarshal([]byte(torrents), &torrentsData)

		// Parse genres
		var genresData []string
		json.Unmarshal([]byte(genres), &genresData)

		favorites = append(favorites, map[string]interface{}{
			"id":                  movieID,
			"title":               title,
			"year":                year,
			"rating":              rating,
			"runtime":             runtime,
			"genres":              genresData,
			"summary":             summary,
			"medium_cover_image":  coverImage,
			"torrents":            torrentsData,
		})
	}

	// Return empty array if no favorites
	if favorites == nil {
		favorites = []map[string]interface{}{}
	}

	respondWithJSON(w, http.StatusOK, favorites)
}

func addFavoriteHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var movie map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&movie); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	// Extract and marshal arrays
	genresJSON, _ := json.Marshal(movie["genres"])
	torrentsJSON, _ := json.Marshal(movie["torrents"])

	_, err := db.Exec(`INSERT OR REPLACE INTO favorites
		(movie_id, title, year, rating, runtime, genres, summary, cover_image, torrents)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		movie["movie_id"], movie["title"], movie["year"], movie["rating"], movie["runtime"],
		string(genresJSON), movie["summary"], movie["cover_image"], string(torrentsJSON))

	if err != nil {
		log.Printf("Error adding favorite: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to add favorite"})
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"message": "Added to favorites"})
}

func removeFavoriteHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		return
	}

	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract movie ID from URL path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 6 {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Movie ID required"})
		return
	}

	movieID := parts[5]

	// Convert string to int to match database INTEGER type
	movieIDInt, err := strconv.Atoi(movieID)
	if err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid movie ID"})
		return
	}

	_, err = db.Exec("DELETE FROM favorites WHERE movie_id = ?", movieIDInt)
	if err != nil {
		log.Printf("Error removing favorite: %v", err)
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to remove favorite"})
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"message": "Removed from favorites"})
}

// Fetch YTS Movies Handler - Uses YTS API directly
func fetchYTSMovies(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get query parameters
	requestedPage := r.URL.Query().Get("page")
	if requestedPage == "" {
		requestedPage = "1"
	}
	pageNum, _ := strconv.Atoi(requestedPage)

	searchQuery := r.URL.Query().Get("query")

	client := createSelectiveProxyClient()

	// Get YTS server URL from settings
	settingsMutex.RLock()
	ytsServerURL := currentSettings.YTSServerURL
	settingsMutex.RUnlock()

	// Default to YTS.mx if not set
	if ytsServerURL == "" {
		ytsServerURL = "https://yts.mx/api/v2/list_movies.json"
	}

	// Build API URL with query parameters
	apiURL := fmt.Sprintf("%s?page=%d&limit=20&sort_by=date_added&order_by=desc", ytsServerURL, pageNum)

	// Add search query if provided
	if searchQuery != "" {
		apiURL = fmt.Sprintf("%s?page=%d&limit=20&query_term=%s", ytsServerURL, pageNum, url.QueryEscape(searchQuery))
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create request"})
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch movies"})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read response"})
		return
	}

	var apiResp map[string]interface{}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to parse response"})
		return
	}

	// Add magnet URLs to torrents
	if data, ok := apiResp["data"].(map[string]interface{}); ok {
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
										magnetLink := fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s+%s&tr=udp://open.demonii.com:1337/announce&tr=udp://tracker.openbittorrent.com:80&tr=udp://tracker.coppersurfer.tk:6969&tr=udp://glotorrents.pw:6969/announce&tr=udp://tracker.opentrackr.org:1337/announce&tr=udp://torrent.gresille.org:80/announce&tr=udp://p4p.arenabg.com:1337&tr=udp://tracker.leechers-paradise.org:6969",
											hash,
											strings.ReplaceAll(title, " ", "+"),
											quality)
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

	respondWithJSON(w, http.StatusOK, apiResp)
}

func fetchMovieTorrents(client *http.Client, title string, movieData map[string]interface{}) []interface{} {
	// Search for movie by title using YTS API
	searchURL := fmt.Sprintf("https://yts.mx/api/v2/list_movies.json?query_term=%s&limit=1", url.QueryEscape(title))

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return []interface{}{}
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return []interface{}{}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []interface{}{}
	}

	var apiResp map[string]interface{}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return []interface{}{}
	}

	// Extract torrents and movie metadata from first matching movie
	if data, ok := apiResp["data"].(map[string]interface{}); ok {
		if movies, ok := data["movies"].([]interface{}); ok && len(movies) > 0 {
			if movie, ok := movies[0].(map[string]interface{}); ok {
				// Update cover images from API
				if img, ok := movie["medium_cover_image"].(string); ok {
					movieData["medium_cover_image"] = img
				}
				if img, ok := movie["large_cover_image"].(string); ok {
					movieData["large_cover_image"] = img
				}

				if torrents, ok := movie["torrents"].([]interface{}); ok {
					// Add magnet links to each torrent
					for _, torrent := range torrents {
						if torrentMap, ok := torrent.(map[string]interface{}); ok {
							if hash, ok := torrentMap["hash"].(string); ok {
								quality := ""
								if q, ok := torrentMap["quality"].(string); ok {
									quality = q
								}
								magnetLink := fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s+%s&tr=udp://open.demonii.com:1337/announce&tr=udp://tracker.openbittorrent.com:80&tr=udp://tracker.coppersurfer.tk:6969&tr=udp://glotorrents.pw:6969/announce&tr=udp://tracker.opentrackr.org:1337/announce&tr=udp://torrent.gresille.org:80/announce&tr=udp://p4p.arenabg.com:1337&tr=udp://tracker.leechers-paradise.org:6969",
									hash,
									strings.ReplaceAll(title, " ", "+"),
									quality)
								torrentMap["magnetUrl"] = magnetLink
							}
						}
					}
					return torrents
				}
			}
		}
	}

	return []interface{}{}
}

func parseYTSMovies(html string) ([]map[string]interface{}, int) {
	var movies []map[string]interface{}
	totalPages := 1

	// Extract total pages from pagination
	// Look for pagination links like ?page=2, ?page=3, etc.
	if idx := strings.Index(html, `class="tsc_pagination`); idx != -1 {
		paginationSection := html[idx:min(idx+2000, len(html))]
		// Find all page numbers
		maxPage := 1
		pageMarkers := strings.Split(paginationSection, `?page=`)
		for _, marker := range pageMarkers {
			if endIdx := strings.IndexAny(marker, `">"`); endIdx != -1 {
				pageNumStr := marker[:endIdx]
				if pageNum, err := strconv.Atoi(pageNumStr); err == nil && pageNum > maxPage {
					maxPage = pageNum
				}
			}
		}
		totalPages = maxPage
	}

	// Split by movie cards
	parts := strings.Split(html, `<div class="browse-movie-wrap`)

	for i := 1; i < len(parts); i++ {
		part := parts[i]

		movie := make(map[string]interface{})

		// Extract movie link and ID
		if idx := strings.Index(part, `href="https://yts.mx/movies/`); idx != -1 {
			linkStart := idx + len(`href="https://yts.mx/movies/`)
			if linkEnd := strings.Index(part[linkStart:], `"`); linkEnd != -1 {
				slug := part[linkStart : linkStart+linkEnd]
				movie["slug"] = slug
			}
		}

		// Extract title
		if idx := strings.Index(part, `class="browse-movie-title"`); idx != -1 {
			titleStart := strings.Index(part[idx:], `>`) + idx + 1
			if titleEnd := strings.Index(part[titleStart:], `</a>`); titleEnd != -1 {
				title := part[titleStart : titleStart+titleEnd]
				// Remove [ZH] tag if present
				title = strings.TrimSpace(strings.ReplaceAll(title, `<span style="color: #ACD7DE; font-size: 75%;">[ZH]</span>`, ""))
				movie["title"] = title
				movie["title_english"] = title
			}
		}

		// Extract year
		if idx := strings.Index(part, `class="browse-movie-year"`); idx != -1 {
			yearStart := strings.Index(part[idx:], `>`) + idx + 1
			if yearEnd := strings.Index(part[yearStart:], `</div>`); yearEnd != -1 {
				year := strings.TrimSpace(part[yearStart : yearStart+yearEnd])
				movie["year"], _ = strconv.Atoi(year)
			}
		}

		// Extract cover image
		if idx := strings.Index(part, `<img src="`); idx != -1 {
			imgStart := idx + len(`<img src="`)
			if imgEnd := strings.Index(part[imgStart:], `"`); imgEnd != -1 {
				imgURL := part[imgStart : imgStart+imgEnd]
				movie["medium_cover_image"] = imgURL
				movie["large_cover_image"] = imgURL
			}
		}

		// Extract rating
		if idx := strings.Index(part, `<h4 class="rating">`); idx != -1 {
			ratingStart := idx + len(`<h4 class="rating">`)
			if ratingEnd := strings.Index(part[ratingStart:], `</h4>`); ratingEnd != -1 {
				ratingStr := strings.TrimSpace(part[ratingStart : ratingStart+ratingEnd])
				ratingStr = strings.ReplaceAll(ratingStr, " / 10", "")
				movie["rating"], _ = strconv.ParseFloat(ratingStr, 64)
			}
		}

		movie["language"] = "zh"

		// For torrents, we'll need to fetch the individual movie page
		// For now, provide empty array - will be populated when user clicks
		movie["torrents"] = []interface{}{}

		if len(movie) > 0 {
			movies = append(movies, movie)
		}
	}

	return movies, totalPages
}

func extractCSRFToken(html string) string {
	// Extract _token from meta tag or input field
	if idx := strings.Index(html, `name="_token" content="`); idx != -1 {
		start := idx + len(`name="_token" content="`)
		if end := strings.Index(html[start:], `"`); end != -1 {
			return html[start : start+end]
		}
	}
	if idx := strings.Index(html, `name="_token" value="`); idx != -1 {
		start := idx + len(`name="_token" value="`)
		if end := strings.Index(html[start:], `"`); end != -1 {
			return html[start : start+end]
		}
	}
	return ""
}

func parseMoviesFromHTML(html string) []map[string]interface{} {
	movies := []map[string]interface{}{}

	// Simple HTML parsing to extract movie data
	// Look for movie browse items
	parts := strings.Split(html, `class="browse-movie-wrap`)

	for i := 1; i < len(parts); i++ {
		movie := make(map[string]interface{})
		part := parts[i]

		// Extract movie title
		if idx := strings.Index(part, `class="browse-movie-title"`); idx != -1 {
			if start := strings.Index(part[idx:], ">")+idx+1; start > idx {
				if end := strings.Index(part[start:], "<")+start; end > start {
					movie["title"] = strings.TrimSpace(part[start:end])
					movie["title_english"] = movie["title"]
					movie["title_long"] = movie["title"]
				}
			}
		}

		// Extract year
		if idx := strings.Index(part, `class="browse-movie-year"`); idx != -1 {
			if start := strings.Index(part[idx:], ">")+idx+1; start > idx {
				if end := strings.Index(part[start:], "<")+start; end > start {
					yearStr := strings.TrimSpace(part[start:end])
					if year, err := strconv.Atoi(yearStr); err == nil {
						movie["year"] = year
					}
				}
			}
		}

		// Extract image
		if idx := strings.Index(part, `<img src="`); idx != -1 {
			start := idx + len(`<img src="`)
			if end := strings.Index(part[start:], `"`); end != -1 {
				imgURL := part[start : start+end]
				movie["medium_cover_image"] = imgURL
				movie["large_cover_image"] = imgURL
			}
		}

		// Extract rating
		movie["rating"] = 0.0
		movie["language"] = "zh"
		movie["state"] = "ok"

		// Extract torrents/download links
		torrents := []map[string]interface{}{}
		if idx := strings.Index(part, `href="magnet:`); idx != -1 {
			start := idx + len(`href="`)
			if end := strings.Index(part[start:], `"`); end != -1 {
				magnetURL := part[start : start+end]
				torrent := map[string]interface{}{
					"url":     magnetURL,
					"quality": "720p",
					"type":    "web",
					"size":    "N/A",
				}
				torrents = append(torrents, torrent)
			}
		}
		movie["torrents"] = torrents

		if len(movie) > 2 {
			movies = append(movies, movie)
		}
	}

	return movies
}

// Fetch Avmoo Movies Handler
func fetchAvmooMovies(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get page parameter
	page := r.URL.Query().Get("page")
	if page == "" {
		page = "1"
	}

	client := createSelectiveProxyClient()

	// Construct URL with page parameter
	fetchURL := fmt.Sprintf("https://avmoo.website/cn/page/%s", page)
	if page == "1" {
		fetchURL = "https://avmoo.website/cn"
	}

	req, err := http.NewRequest("GET", fetchURL, nil)
	if err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create request"})
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch page: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Server returned status %d", resp.StatusCode)})
		return
	}

	htmlBody, err := io.ReadAll(resp.Body)
	if err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read response"})
		return
	}

	// Parse HTML to extract movie data
	movies := parseAvmooMovies(string(htmlBody))

	response := map[string]interface{}{
		"status": "ok",
		"data": map[string]interface{}{
			"page":   page,
			"movies": movies,
		},
	}

	respondWithJSON(w, http.StatusOK, response)
}

func parseAvmooMovies(html string) []map[string]interface{} {
	var movies []map[string]interface{}

	// Look for movie items - they are typically in <div> or <a> tags with movie info
	// Parse each movie block
	parts := strings.Split(html, `<a class="movie-box"`)

	for i := 1; i < len(parts); i++ {
		part := parts[i]
		movie := make(map[string]interface{})

		// Extract movie link/ID
		if idx := strings.Index(part, `href="`); idx != -1 {
			linkStart := idx + len(`href="`)
			if linkEnd := strings.Index(part[linkStart:], `"`); linkEnd != -1 {
				link := part[linkStart : linkStart+linkEnd]
				movie["link"] = link
				// Extract ID from link if present
				if strings.Contains(link, "/movie/") {
					idParts := strings.Split(link, "/movie/")
					if len(idParts) > 1 {
						movie["id"] = idParts[1]
					}
				}
			}
		}

		// Extract cover image
		if idx := strings.Index(part, `<img src="`); idx != -1 {
			imgStart := idx + len(`<img src="`)
			if imgEnd := strings.Index(part[imgStart:], `"`); imgEnd != -1 {
				imgURL := part[imgStart : imgStart+imgEnd]
				movie["cover"] = imgURL
			}
		}

		// Extract title
		if idx := strings.Index(part, `<span class="video-title"`); idx != -1 {
			titleStart := strings.Index(part[idx:], `>`) + idx + 1
			if titleEnd := strings.Index(part[titleStart:], `</span>`); titleEnd != -1 {
				title := strings.TrimSpace(part[titleStart : titleStart+titleEnd])
				movie["title"] = title
			}
		}

		// Extract date
		if idx := strings.Index(part, `<date>`); idx != -1 {
			dateStart := idx + len(`<date>`)
			if dateEnd := strings.Index(part[dateStart:], `</date>`); dateEnd != -1 {
				date := strings.TrimSpace(part[dateStart : dateStart+dateEnd])
				movie["date"] = date
			}
		}

		// For now, we'll fetch magnet links separately when user clicks on a movie
		// because they're typically on the detail page
		movie["magnetUrl"] = ""

		if len(movie) > 0 {
			movies = append(movies, movie)
		}
	}

	return movies
}

// Fetch Avmoo Movie Detail (including magnet link)
func fetchAvmooMovieDetail(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract movie ID from URL path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 6 {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing movie ID"})
		return
	}
	movieID := parts[5]

	client := createSelectiveProxyClient()

	// Construct movie detail URL
	fetchURL := fmt.Sprintf("https://avmoo.website/cn/movie/%s", movieID)

	req, err := http.NewRequest("GET", fetchURL, nil)
	if err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create request"})
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch page: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Server returned status %d", resp.StatusCode)})
		return
	}

	htmlBody, err := io.ReadAll(resp.Body)
	if err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read response"})
		return
	}

	// Parse HTML to extract movie details and magnet link
	movieDetail := parseAvmooMovieDetail(string(htmlBody))

	response := map[string]interface{}{
		"status": "ok",
		"data":   movieDetail,
	}

	respondWithJSON(w, http.StatusOK, response)
}

func parseAvmooMovieDetail(html string) map[string]interface{} {
	movie := make(map[string]interface{})

	// Extract title
	if idx := strings.Index(html, `<h3>`); idx != -1 {
		titleStart := idx + len(`<h3>`)
		if titleEnd := strings.Index(html[titleStart:], `</h3>`); titleEnd != -1 {
			title := strings.TrimSpace(html[titleStart : titleStart+titleEnd])
			movie["title"] = title
		}
	}

	// Extract cover image
	if idx := strings.Index(html, `<img class="bigImage"`); idx != -1 {
		if imgIdx := strings.Index(html[idx:], `src="`); imgIdx != -1 {
			imgStart := idx + imgIdx + len(`src="`)
			if imgEnd := strings.Index(html[imgStart:], `"`); imgEnd != -1 {
				imgURL := html[imgStart : imgStart+imgEnd]
				movie["cover"] = imgURL
			}
		}
	}

	// Extract direct magnet link (if available)
	if idx := strings.Index(html, `href="magnet:`); idx != -1 {
		magnetStart := idx + len(`href="`)
		if magnetEnd := strings.Index(html[magnetStart:], `"`); magnetEnd != -1 {
			magnetURL := html[magnetStart : magnetStart+magnetEnd]
			movie["magnetUrl"] = magnetURL
		}
	}

	// Extract torrent search link (btsow.lol or similar)
	if idx := strings.Index(html, `href="https://btsow.lol/#/search/`); idx != -1 {
		searchStart := idx + len(`href="`)
		if searchEnd := strings.Index(html[searchStart:], `"`); searchEnd != -1 {
			searchURL := html[searchStart : searchStart+searchEnd]
			movie["torrentSearchUrl"] = searchURL

			// Extract the search query from the URL
			if strings.Contains(searchURL, "/search/") {
				parts := strings.Split(searchURL, "/search/")
				if len(parts) > 1 {
					query := parts[1]
					movie["searchQuery"] = query
					// Note: btsow.lol is a SPA, so we can't fetch magnets server-side
					// User needs to click the torrentSearchUrl to get magnets
				}
			}
		}
	}

	// Extract additional info if available
	if idx := strings.Index(html, `<span class="header">發行日期:`); idx != -1 {
		dateStart := strings.Index(html[idx:], `</span>`) + idx + len(`</span>`)
		if dateEnd := strings.Index(html[dateStart:], `</p>`); dateEnd != -1 {
			date := strings.TrimSpace(html[dateStart : dateStart+dateEnd])
			movie["releaseDate"] = date
		}
	}

	return movie
}

func fetchMagnetsFromBtsow(query string) []string {
	var magnets []string

	client := createSelectiveProxyClient()

	// Try to fetch HTML search page
	searchURL := fmt.Sprintf("https://btsow.lol/search/%s", url.QueryEscape(query))

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		log.Printf("Error creating btsow request: %v", err)
		return magnets
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error fetching from btsow: %v", err)
		return magnets
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Btsow returned status %d", resp.StatusCode)
		return magnets
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading btsow response: %v", err)
		return magnets
	}

	html := string(body)

	// Look for magnet links in HTML
	magnetPrefix := "magnet:?xt=urn:btih:"
	parts := strings.Split(html, magnetPrefix)

	for i := 1; i < len(parts); i++ {
		// Find the end of the magnet link (usually at quote or &)
		end := strings.IndexAny(parts[i], `"'<>&`)
		if end == -1 {
			end = 200 // Limit length
		}
		if end > len(parts[i]) {
			end = len(parts[i])
		}

		magnetHash := parts[i][:end]
		magnetURL := magnetPrefix + magnetHash

		// Only add unique magnets
		isDuplicate := false
		for _, existing := range magnets {
			if existing == magnetURL {
				isDuplicate = true
				break
			}
		}

		if !isDuplicate && len(magnetURL) > 50 {
			magnets = append(magnets, magnetURL)
		}

		// Limit to 10 results
		if len(magnets) >= 10 {
			break
		}
	}

	log.Printf("Found %d magnet links for query: %s", len(magnets), query)
	return magnets
}

// Convert Torrent to Magnet Handler
func convertTorrentToMagnetHandler(w http.ResponseWriter, r *http.Request) {
	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form with 10MB memory limit
	const maxUploadSize = 10 << 20 // 10MB
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Failed to parse form: " + err.Error()})
		return
	}

	// Get the torrent file from the form data
	file, header, err := r.FormFile("torrent")
	if err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing torrent file"})
		return
	}
	defer file.Close()

	// Check file size
	if header.Size > maxUploadSize {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "File too large"})
		return
	}

	// Read the torrent file content
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		respondWithJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read file"})
		return
	}

	// Parse torrent file
	mi, err := metainfo.Load(bytes.NewReader(fileBytes))
	if err != nil {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid torrent file: " + err.Error()})
		return
	}

	// Get info hash
	infoHash := mi.HashInfoBytes().String()

	// Build magnet URL components
	magnet := fmt.Sprintf("magnet:?xt=urn:btih:%s", infoHash)

	// Add display name
	info, err := mi.UnmarshalInfo()
	if err == nil {
		magnet += fmt.Sprintf("&dn=%s", url.QueryEscape(info.Name))
	}

	// Add trackers
	for _, tier := range mi.AnnounceList {
		for _, tracker := range tier {
			magnet += fmt.Sprintf("&tr=%s", url.QueryEscape(tracker))
		}
	}

	respondWithJSON(w, http.StatusOK, map[string]string{
		"magnet": magnet,
	})
}
