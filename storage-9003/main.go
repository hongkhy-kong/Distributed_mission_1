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

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	os.MkdirAll(storagePath, os.ModePerm)

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

	_, err = io.Copy(dst, file)
	if err != nil {
		http.Error(w, "Write error", http.StatusInternalServerError)
		return
	}

	w.Write([]byte("OK|" + header.Filename))
}

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
	err = os.Remove(fullPath)
	if err != nil {
		fmt.Println("Delete failed:", fullPath, err)
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	fmt.Println("Deleted:", fullPath)
	w.Write([]byte("Deleted " + filename))
}

func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	files, _ := os.ReadDir(storagePath)
	var list []string
	for _, f := range files {
		list = append(list, f.Name())
	}
	json.NewEncoder(w).Encode(list)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9003" // override per instance
	}

	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/delete", deleteHandler)
	http.HandleFunc("/files", listFilesHandler)
	http.Handle("/files/", http.StripPrefix("/files/", http.FileServer(http.Dir(storagePath))))

	fmt.Printf("Storage server listening :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
