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
	{URL: "http://68.183.231.211:9001", Lat: 1.3521, Lon: 103.8198},  // Singapore
	{URL: "http://167.71.177.212:9002", Lat: 40.7128, Lon: -74.0060}, // New York
	{URL: "http://159.65.48.116:9003", Lat: 51.5074, Lon: -0.1278},   // London
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

// Approximate location for testing
func approximateLocation(ip string) (float64, float64) {
	if strings.HasPrefix(ip, "127.") {
		return 1.3521, 103.8198 // SG
	}
	if strings.HasPrefix(ip, "192.168.1.") {
		return 40.7128, -74.0060 // NY
	}
	return 51.5074, -0.1278 // London
}

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
// Handlers
// ---------------------------
func nearestViewHandler(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		http.Error(w, "filename required", http.StatusBadRequest)
		return
	}

	clientIP := getClientIP(r)
	lat, lon := approximateLocation(clientIP)

	type DistanceInfo struct {
		URL       string
		Port      string
		Distance  float64
		IsNearest bool
	}

	var distances []DistanceInfo
	minDist := math.MaxFloat64
	var nearest StorageServer

	for _, s := range storages {
		d := haversineKm(lat, lon, s.Lat, s.Lon)
		u, _ := url.Parse(s.URL)

		info := DistanceInfo{
			URL:      s.URL,
			Port:     u.Port(),
			Distance: d,
		}

		if d < minDist {
			minDist = d
			nearest = s
		}

		distances = append(distances, info)
	}

	for i := range distances {
		if distances[i].URL == nearest.URL {
			distances[i].IsNearest = true
		}
	}

	previewURL := nearest.URL + "/files/" + filename
	u, _ := url.Parse(nearest.URL)

	data := struct {
		Filename    string
		PreviewURL  string
		NearestPort string
		Distances   []DistanceInfo
	}{
		Filename:    filename,
		PreviewURL:  previewURL,
		NearestPort: u.Port(),
		Distances:   distances,
	}

	if err := templates.ExecuteTemplate(w, "nearest.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

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

	os.MkdirAll("uploads", 0755)
	dstPath := filepath.Join("uploads", filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		http.Error(w, "Cannot save file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	_, _ = dst.Write(fileBytes)

	// Replicate
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

func deleteHandler(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		http.Error(w, "filename required", http.StatusBadRequest)
		return
	}

	os.Remove(filepath.Join("uploads", filename))

	encodedName := url.QueryEscape(filename)
	for _, s := range storages {
		http.Get(s.URL + "/delete?filename=" + encodedName)
	}

	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

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
		for i, s := range storages {
			for _, r := range allStorage[s.URL] {
				if r == f.Name() {
					replica[fmt.Sprintf("900%d", i+1)] = true
				}
			}
		}
		out = append(out, FileInfo{Name: f.Name(), Replica: replica})
	}

	clientIP := getClientIP(r)
	lat, lon := approximateLocation(clientIP)
	nearestURL := getNearestStorage(lat, lon)

	nearestPort := ""
	for i, s := range storages {
		if s.URL == nearestURL {
			nearestPort = fmt.Sprintf("900%d", i+1)
			break
		}
	}

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
