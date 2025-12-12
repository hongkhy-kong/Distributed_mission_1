package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------
// Template Loader
// ---------------------------
var templates = template.Must(template.ParseGlob("templates/*.html"))

// ---------------------------
// Storage Servers
// ---------------------------
type StorageServer struct {
	URL string
	Lat float64
	Lon float64
}

var storages = []StorageServer{
	{URL: "http://localhost:9001", Lat: 1.3521, Lon: 103.8198},  // Singapore
	{URL: "http://localhost:9002", Lat: 40.7128, Lon: -74.0060}, // New York
	{URL: "http://localhost:9003", Lat: 51.5074, Lon: -0.1278},  // London
}

// ---------------------------
// Helpers
// ---------------------------
func forwardFileTo(url, filename string, fileBytes []byte) (int, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return 0, "", err
	}
	_, err = part.Write(fileBytes)
	if err != nil {
		return 0, "", err
	}
	writer.Close()

	req, err := http.NewRequest("POST", url+"/upload", body)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0 // Earth radius km
	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLon := (lon2 - lon1) * math.Pi / 180.0
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180.0)*math.Cos(lat2*math.Pi/180.0)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

// Get client IP
func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xr := r.Header.Get("X-Real-Ip"); xr != "" {
		return strings.TrimSpace(xr)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Approximate IP location (for local testing)
func approximateLocation(ip string) (float64, float64) {
	if strings.HasPrefix(ip, "127.") {
		return 1.3521, 103.8198 // Singapore
	}
	if strings.HasPrefix(ip, "192.168.1.") {
		return 40.7128, -74.0060 // New York
	}
	return 51.5074, -0.1278 // London
}

// Get nearest storage based on lat/lon
func getNearestStorage(lat, lon float64) string {
	nearest := ""
	minDist := 999999.0

	for _, s := range storages {
		d := haversineKm(lat, lon, s.Lat, s.Lon)
		if d < minDist {
			minDist = d
			nearest = s.URL
		}
	}
	return nearest
}

// ---------------------------
// Nearest File Handler
// ---------------------------
func nearestViewHandler(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		http.Error(w, "filename required", http.StatusBadRequest)
		return
	}

	// Determine client location
	clientIP := getClientIP(r)
	lat, lon := approximateLocation(clientIP)

	type DistanceInfo struct {
		URL       string
		Port      string
		Distance  float64
		IsNearest bool
	}

	distances := []DistanceInfo{}
	nearestURL := ""
	minDist := 999999.0

	// Calculate distances
	for _, s := range storages {
		d := haversineKm(lat, lon, s.Lat, s.Lon)
		distInfo := DistanceInfo{
			URL:      s.URL,
			Port:     strings.TrimPrefix(s.URL, "http://localhost:"),
			Distance: d,
		}

		if d < minDist {
			minDist = d
			nearestURL = s.URL
		}

		distances = append(distances, distInfo)
	}

	// Mark nearest
	for i := range distances {
		if distances[i].URL == nearestURL {
			distances[i].IsNearest = true
		}
	}

	previewURL := nearestURL + "/files/" + filename
	nearestPort := strings.TrimPrefix(nearestURL, "http://localhost:")

	// Pass to template
	data := struct {
		Filename    string
		PreviewURL  string
		NearestPort string
		Distances   []DistanceInfo
	}{
		Filename:    filename,
		PreviewURL:  previewURL,
		NearestPort: nearestPort,
		Distances:   distances,
	}

	templates.ExecuteTemplate(w, "nearest.html", data)
}

// ---------------------------
// Upload Handler
// ---------------------------
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseMultipartForm(100 << 20)
	if err != nil {
		http.Error(w, "Parse error: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Read error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	filename := filepath.Base(header.Filename)

	// Save locally
	os.MkdirAll("uploads", 0755)
	dstPath := filepath.Join("uploads", filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		http.Error(w, "Cannot save file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	_, _ = dst.Write(fileBytes)

	// Replicate to storage servers
	for _, s := range storages {
		status, body, err := forwardFileTo(s.URL, filename, fileBytes)
		if err != nil {
			fmt.Println("Replication error to", s.URL, ":", err)
		} else {
			fmt.Println("Replicated to", s.URL, "Status:", status, "Body:", body)
		}
	}

	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

// ---------------------------
// Delete Handler
// ---------------------------
func deleteHandler(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		http.Error(w, "filename required", http.StatusBadRequest)
		return
	}

	// Delete local
	localPath := filepath.Join("uploads", filename)
	err := os.Remove(localPath)
	if err != nil {
		fmt.Println("Local delete error:", err)
	}

	// Delete on storage servers
	encodedName := url.QueryEscape(filename)
	for _, s := range storages {
		urlToCall := s.URL + "/delete?filename=" + encodedName
		resp, err := http.Get(urlToCall)
		if err != nil {
			fmt.Println("Delete error on", s.URL, ":", err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Println("Deleted on", s.URL, "Status:", resp.StatusCode, "Body:", string(body))
	}

	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

// ---------------------------
// List Files Handler
// ---------------------------
func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	files, _ := ioutil.ReadDir("uploads")

	type FileInfo struct {
		Name    string
		Replica map[string]bool
	}

	var out []FileInfo
	allStorage := map[string][]string{}

	for _, s := range storages {
		resp, err := http.Get(s.URL + "/files")
		var list []string
		if err == nil {
			json.NewDecoder(resp.Body).Decode(&list)
			resp.Body.Close()
		}
		allStorage[s.URL] = list
	}

	for _, f := range files {
		replica := map[string]bool{
			"9001": false,
			"9002": false,
			"9003": false,
		}
		for _, s := range storages {
			for _, r := range allStorage[s.URL] {
				if r == f.Name() {
					if s.URL == "http://localhost:9001" {
						replica["9001"] = true
					}
					if s.URL == "http://localhost:9002" {
						replica["9002"] = true
					}
					if s.URL == "http://localhost:9003" {
						replica["9003"] = true
					}
				}
			}
		}
		out = append(out, FileInfo{Name: f.Name(), Replica: replica})
	}

	// ------------------------------
	// Determine nearest storage
	// ------------------------------
	clientIP := getClientIP(r)
	lat, lon := approximateLocation(clientIP)
	nearestURL := getNearestStorage(lat, lon)
	nearestPort := ""
	switch nearestURL {
	case "http://localhost:9001":
		nearestPort = "9001"
	case "http://localhost:9002":
		nearestPort = "9002"
	case "http://localhost:9003":
		nearestPort = "9003"
	}

	// Pass data to template
	data := struct {
		Files         []FileInfo
		NearestServer string
	}{
		Files:         out,
		NearestServer: nearestPort,
	}

	templates.ExecuteTemplate(w, "list.html", data)
}

// ---------------------------
// Serve Uploads
// ---------------------------
func serveUploads() {
	os.MkdirAll("uploads", 0755)
	http.Handle("/files/", http.StripPrefix("/files/", http.FileServer(http.Dir("uploads"))))
}

// ---------------------------
// Home Page
// ---------------------------
func homePage(w http.ResponseWriter, r *http.Request) {
	templates.ExecuteTemplate(w, "upload.html", nil)
}

// ---------------------------
// Main
// ---------------------------
func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	serveUploads()

	http.HandleFunc("/", homePage)
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/delete", deleteHandler)
	http.HandleFunc("/files", listFilesHandler)
	http.HandleFunc("/nearest-view", nearestViewHandler)

	fmt.Println("Central API listening on :" + port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
