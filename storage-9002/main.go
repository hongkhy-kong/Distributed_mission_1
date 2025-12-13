package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

var storagePath = "files"

func main() {
	// Set port per instance
	port := os.Getenv("PORT")
	if port == "" {
		port = "9001" // default port, override for each droplet
	}

	// Ensure storage directory exists
	if err := os.MkdirAll(storagePath, 0755); err != nil {
		log.Fatalf("Failed to create storage directory: %v", err)
	}

	// Routes
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/delete", deleteHandler)
	http.HandleFunc("/files", listFilesHandler)                                                 // JSON list
	http.Handle("/files/", http.StripPrefix("/files/", http.FileServer(http.Dir(storagePath)))) // serve actual files

	fmt.Printf("Storage server listening on port %s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// Upload a file to storage
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "Parse error: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	dstPath := filepath.Join(storagePath, filepath.Base(header.Filename))
	dst, err := os.Create(dstPath)
	if err != nil {
		http.Error(w, "Cannot create file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Write error", http.StatusInternalServerError)
		return
	}

	fmt.Printf("Uploaded: %s\n", dstPath)
	w.Write([]byte("OK|" + header.Filename))
}

// Delete a file from storage
func deleteHandler(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("filename")
	if raw == "" {
		http.Error(w, "filename required", http.StatusBadRequest)
		return
	}

	filename, err := url.QueryUnescape(raw)
	if err != nil {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	fullPath := filepath.Join(storagePath, filename)
	if err := os.Remove(fullPath); err != nil {
		fmt.Println("Delete failed:", fullPath, err)
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	fmt.Println("Deleted:", fullPath)
	w.Write([]byte("Deleted " + filename))
}

// List all files as JSON
func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	files, err := os.ReadDir(storagePath)
	if err != nil {
		http.Error(w, "Cannot read directory", http.StatusInternalServerError)
		return
	}

	var list []string
	for _, f := range files {
		list = append(list, f.Name())
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}
