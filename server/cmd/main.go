package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"server/internal/server"
	"server/internal/server/clients"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// If the server is running in a Docker container, the data directory is always mounted at this path
const (
	dockerMountedDataDir  = "/gameserver/data"
	dockerMountedCertsDir = "/gameserver/certs"
)

type config struct {
	Port       int
	DataPath   string
	CertPath   string
	KeyPath    string
	ClientPath string
}

var (
	defaultConfig = &config{Port: 8080}
	configPath    = flag.String("config", ".env", "Path to the config file")
)

func loadConfig() *config {
	cfg := defaultConfig
	cfg.DataPath = os.Getenv("DATA_PATH")
	cfg.CertPath = os.Getenv("CERT_PATH")
	cfg.KeyPath = os.Getenv("KEY_PATH")
	cfg.ClientPath = os.Getenv("CLIENT_PATH")

	port, err := strconv.Atoi(os.Getenv("PORT"))
	if err != nil {
		log.Printf("Error parsing PORT, using %d", cfg.Port)
		return cfg
	}

	cfg.Port = port

	return cfg
}

func coalescePaths(fallbacks ...string) string {
	for i, path := range fallbacks {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			message := fmt.Sprintf("File/folder not found at %s", path)
			if i < len(fallbacks)-1 {
				log.Printf("%s - going to try %s", message, fallbacks[i+1])
			} else {
				log.Printf("%s - no more fallbacks to try", message)
			}
		} else {
			log.Printf("File/folder found at %s", path)
			return path
		}
	}
	return ""
}

func resolveLiveCertsPath(certPath string) string {
	normalizedPath := strings.ReplaceAll(certPath, "\\", "/")
	pathComponents := strings.Split(normalizedPath, "/live/")

	if len(pathComponents) >= 2 {
		pathTail := pathComponents[len(pathComponents)-1]

		// Try to load the certificates exactly as they appear in the config,
		// otherwise assume they are in the Docker-mounted folder for certs
		return coalescePaths(certPath, filepath.Join(dockerMountedCertsDir, "live", pathTail))
	}

	return certPath
}

func main() {
	flag.Parse()
	err := godotenv.Load(*configPath)
	cfg := defaultConfig
	if err != nil {
		log.Printf("Error loading config file, defaulting to %+v", defaultConfig)
	} else {
		cfg = loadConfig()
	}

	// Try to load the Docker-mounted data directory. If that fails, fall back
	// to the current directory
	cfg.DataPath = coalescePaths(cfg.DataPath, dockerMountedDataDir, ".")

	// Define the game hub
	hub := server.NewHub(cfg.DataPath)

	// Define handler for serving the HTML5 export
	exportPath := coalescePaths(cfg.ClientPath, filepath.Join(cfg.DataPath, "html5"))
	if _, err := os.Stat(exportPath); err != nil {
		if os.IsNotExist(err) {
			log.Printf("WARNING: HTML5 export directory not found at %s. Root path '/' will not be served.", exportPath)
		} else {
			// Log other errors related to accessing the path, but don't exit
			log.Printf("ERROR: Could not access HTML5 export path %s: %v", exportPath, err)
		}
	} else {
		log.Printf("Serving HTML5 export from %s", exportPath)
		// Add logging to the file server handler
		fileServerHandler := http.StripPrefix("/", http.FileServer(http.Dir(exportPath)))
		loggedFileServerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("HTTP Request: Method=%s Host=%s Path=%s RemoteAddr=%s", r.Method, r.Host, r.URL.Path, r.RemoteAddr)
			fileServerHandler.ServeHTTP(w, r)
		})
		http.Handle("/", addHeaders(loggedFileServerHandler)) // Use the logged handler
	}

	// Define handler for WebSocket connections
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		hub.Serve(clients.NewWebSocketClient, w, r)
	})

	go hub.Run()
	addr := fmt.Sprintf(":%d", cfg.Port)

	log.Printf("Starting server on %s", addr)

	// cfg.CertPath = resolveLiveCertsPath(cfg.CertPath)
	// cfg.KeyPath = resolveLiveCertsPath(cfg.KeyPath)

	// log.Printf("Using cert at %s and key at %s", cfg.CertPath, cfg.KeyPath)

	// err = http.ListenAndServeTLS(addr, cfg.CertPath, cfg.KeyPath, nil)

	// if err != nil {
		log.Println("Starting server without TLS (forced)")
		err = http.ListenAndServe(addr, nil)
		if err != nil {
			log.Fatalf("Failed to start server without TLS: %v", err)
		// }
	}
}

// Add headers required for the HTML5 export to work with threads
func addHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		next.ServeHTTP(w, r)
	})
}
