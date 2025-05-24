package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kardianos/service"
	_ "github.com/mattn/go-sqlite3"
	"github.com/xuri/excelize/v2"
)

const (
	DB_PATH = "finder.db"
)

type SearchRequest struct {
	Directories []string `json:"directories"`
	Query       string   `json:"query"`
	Extensions  []string `json:"extensions"`
}

type Match struct {
	File    string `json:"file"`
	Sheet   string `json:"sheet"`
	Row     int    `json:"row"`
	Content string `json:"content"`
}

type SearchResponse struct {
	Matches []Match `json:"matches"`
}

type ImportResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

var db *sql.DB

type program struct {
	server *http.Server
}

func (p *program) Start(s service.Service) error {
	go p.run()
	return nil
}

func (p *program) Stop(s service.Service) error {
	if p.server != nil {
		return p.server.Close()
	}
	return nil
}

func (p *program) run() {
	// Get the current working directory
	workDir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	// Print debug information
	fmt.Printf("Working directory: %s\n", workDir)
	staticDir := filepath.Join(workDir, "static")
	fmt.Printf("Static directory: %s\n", staticDir)

	// Verify static directory exists
	if _, err := os.Stat(staticDir); os.IsNotExist(err) {
		log.Fatalf("Static directory does not exist: %s", staticDir)
	}

	// Set up static file server with logging
	fs := http.FileServer(http.Dir(staticDir))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	// Add root handler to serve index.html with logging
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("Received request for path: %s\n", r.URL.Path)
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			indexPath := filepath.Join(staticDir, "index.html")
			fmt.Printf("Serving index.html from: %s\n", indexPath)
			http.ServeFile(w, r, indexPath)
		} else {
			fmt.Printf("Path not found: %s\n", r.URL.Path)
			http.NotFound(w, r)
		}
	})

	http.HandleFunc("/search", searchHandler)
	http.HandleFunc("/import", importHandler)

	// Create server
	p.server = &http.Server{
		Addr: ":8080",
	}

	fmt.Println("Server running at http://localhost:8080/static/")
	if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func init() {
	var err error
	// Open database with WAL mode
	db, err = sql.Open("sqlite3", DB_PATH+"?_pragma=journal_mode(WAL)")
	if err != nil {
		log.Fatal(err)
	}

	// Create FTS4 table if it doesn't exist
	_, err = db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS files_fts USING fts4(
			file,
			sheet,
			row,
			content
		)
	`)
	if err != nil {
		log.Fatal(err)
	}

	// Optimize FTS4
	_, err = db.Exec("INSERT INTO files_fts(files_fts) VALUES('optimize')")
	if err != nil && !strings.Contains(err.Error(), "no such table") {
		log.Printf("Warning: Could not optimize FTS4: %v", err)
	}
}

func importToSQLite(dirs []string, extensions []string) error {
	// Clear existing data
	_, err := db.Exec("DELETE FROM files_fts")
	if err != nil {
		return fmt.Errorf("error clearing existing data: %v", err)
	}

	// Begin transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO files_fts (file, sheet, row, content)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("error preparing statement: %v", err)
	}
	defer stmt.Close()

	for _, dir := range dirs {
		absPath, err := filepath.Abs(dir)
		if err != nil {
			fmt.Printf("Error getting absolute path for %s: %v\n", dir, err)
			continue
		}

		filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				ext := strings.ToLower(filepath.Ext(d.Name()))
				if len(ext) > 0 {
					ext = ext[1:]
				}
				for _, allowedExt := range extensions {
					if ext == allowedExt {
						if ext == "csv" {
							docs, err := readCSVFile(path)
							if err == nil {
								for _, doc := range docs {
									_, err = stmt.Exec(doc["file"], doc["sheet"], doc["row"], doc["content"])
									if err != nil {
										return fmt.Errorf("error inserting CSV data: %v", err)
									}
								}
							}
						} else {
							docs, err := readExcelFile(path)
							if err == nil {
								for _, doc := range docs {
									_, err = stmt.Exec(doc["file"], doc["sheet"], doc["row"], doc["content"])
									if err != nil {
										return fmt.Errorf("error inserting Excel data: %v", err)
									}
								}
							}
						}
						break
					}
				}
			}
			return nil
		})
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %v", err)
	}

	return nil
}

func readCSVFile(path string) ([]map[string]interface{}, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	var documents []map[string]interface{}
	for rowNum, row := range rows {
		if len(row) == 0 {
			continue
		}
		doc := map[string]interface{}{
			"file":    path,
			"sheet":   "Sheet1",
			"row":     rowNum + 1,
			"content": strings.Join(row, " - "),
		}
		documents = append(documents, doc)
	}
	return documents, nil
}

func readExcelFile(path string) ([]map[string]interface{}, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var documents []map[string]interface{}
	rowNum := 1

	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}

		for _, row := range rows {
			if len(row) == 0 {
				continue
			}
			doc := map[string]interface{}{
				"file":    path,
				"sheet":   sheet,
				"row":     rowNum,
				"content": strings.Join(row, " - "),
			}
			documents = append(documents, doc)
			rowNum++
		}
	}
	return documents, nil
}

func searchInSQLite(query string) ([]Match, error) {
	if query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}

	rows, err := db.Query(`
		SELECT file, sheet, row, content
		FROM files_fts
		WHERE files_fts MATCH ?
		LIMIT 1000
	`, query)
	if err != nil {
		return nil, fmt.Errorf("database error: %v", err)
	}
	defer rows.Close()

	var matches []Match
	for rows.Next() {
		var match Match
		err := rows.Scan(&match.File, &match.Sheet, &match.Row, &match.Content)
		if err != nil {
			return nil, fmt.Errorf("error scanning results: %v", err)
		}
		matches = append(matches, match)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating results: %v", err)
	}

	return matches, nil
}

func importHandler(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	log.Printf("Import request received at %v", startTime.Format(time.RFC3339))

	if r.Method != http.MethodPost {
		log.Printf("Invalid method: %s", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Invalid request body: %v", err)
		http.Error(w, "invalid request", 400)
		return
	}

	log.Printf("Import request: directories=%v, extensions=%v", req.Directories, req.Extensions)

	err := importToSQLite(req.Directories, req.Extensions)
	if err != nil {
		log.Printf("Import error: %v", err)
		resp := ImportResponse{
			Status:  "error",
			Message: err.Error(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	duration := time.Since(startTime)
	log.Printf("Import completed in %v", duration)

	resp := ImportResponse{
		Status:  "success",
		Message: fmt.Sprintf("Data imported successfully in %v", duration),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	log.Printf("Search request received at %v", startTime.Format(time.RFC3339))

	if r.Method != http.MethodPost {
		log.Printf("Invalid method: %s", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Invalid request body: %v", err)
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if req.Query == "" {
		log.Printf("Empty search query")
		http.Error(w, "Search query cannot be empty", http.StatusBadRequest)
		return
	}

	log.Printf("Search request: query=%q, directories=%v, extensions=%v",
		req.Query, req.Directories, req.Extensions)

	matches, err := searchInSQLite(req.Query)
	if err != nil {
		log.Printf("Search error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	duration := time.Since(startTime)
	log.Printf("Search completed in %v, found %d matches", duration, len(matches))

	resp := SearchResponse{Matches: matches}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("Error encoding response: %v", err)
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		return
	}
}

func main() {
	svcConfig := &service.Config{
		Name:        "ExcelFileFinder",
		DisplayName: "Excel File Finder Service",
		Description: "A service for searching and importing Excel files.",
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatal(err)
	}

	if len(os.Args) > 1 {
		err = service.Control(s, os.Args[1])
		if err != nil {
			fmt.Printf("Valid actions: %q\n", service.ControlAction)
			log.Fatal(err)
		}
		return
	}

	err = s.Run()
	if err != nil {
		log.Fatal(err)
	}
}
