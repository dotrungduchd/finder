package main

import (
	"bufio"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/extrame/xls"
	"github.com/kardianos/service"
	_ "github.com/mattn/go-sqlite3"
	"github.com/xuri/excelize/v2"
)

const (
	DB_PATH       = "finder.db"
	EMAIL_DB_PATH = "finder-email.db"
)

type SearchRequest struct {
	Directories []string `json:"directories"`
	Query       string   `json:"query"`
	Extensions  []string `json:"extensions"`
	Page        int      `json:"page"`
	PageSize    int      `json:"pageSize"`
	EmailOnly   bool     `json:"emailOnly"`
}

type ImportRequest struct {
	Files      []string `json:"files"`
	Extensions []string `json:"extensions"`
	ResetDB    bool     `json:"resetDB"`
	EmailOnly  bool     `json:"emailOnly"`
}

type CheckFilesRequest struct {
	Files     []string `json:"files"`
	EmailOnly bool     `json:"emailOnly"`
}

type CheckFilesResponse struct {
	ImportedFiles    []string `json:"importedFiles"`
	NotImportedFiles []string `json:"notImportedFiles"`
}

type Match struct {
	File    string `json:"file"`
	Sheet   string `json:"sheet"`
	Row     int    `json:"row"`
	Email   string `json:"email"`
	Content string `json:"content"`
}

type SearchResponse struct {
	Matches     []Match `json:"matches"`
	TotalCount  int     `json:"totalCount"`
	TotalPages  int     `json:"totalPages"`
	CurrentPage int     `json:"currentPage"`
}

type ImportResponse struct {
	Status      string `json:"status"`
	Message     string `json:"message"`
	TotalRows   int    `json:"totalRows"`
	TotalFiles  int    `json:"totalFiles"`
	FailedFiles int    `json:"failedFiles"`
}

type StatusRequest struct {
	EmailOnly bool `json:"emailOnly"`
}

type StatusResponse struct {
	TotalRows   int    `json:"totalRows"`
	TotalFiles  int    `json:"totalFiles"`
	FailedFiles int    `json:"failedFiles"`
	LastImport  string `json:"lastImport"`
	DBSize      string `json:"dbSize"`
}

var db *sql.DB
var emailDB *sql.DB

type program struct {
	server *http.Server
}

func (p *program) Start(s service.Service) error {
	log.Printf("Service starting...")
	go p.run()
	return nil
}

func (p *program) Stop(s service.Service) error {
	log.Printf("Service stopping...")
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

	log.Printf("Service started in directory: %s", workDir)

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
	http.HandleFunc("/check-files", checkFilesHandler)
	http.HandleFunc("/status", statusHandler)

	// Create server
	p.server = &http.Server{
		Addr: ":8080",
	}

	log.Printf("Server starting at http://localhost:8080/static/")
	fmt.Println("Server running at http://localhost:8080/static/")
	if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func init() {
	var err error
	// Open main database with optimized settings
	db, err = sql.Open("sqlite3", DB_PATH+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)&_pragma=mmap_size(30000000000)&_pragma=cache_size(-2000)&_pragma=page_size(4096)")
	if err != nil {
		log.Fatal(err)
	}

	// Open email database with optimized settings
	emailDB, err = sql.Open("sqlite3", EMAIL_DB_PATH+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)&_pragma=mmap_size(30000000000)&_pragma=cache_size(-2000)&_pragma=page_size(4096)")
	if err != nil {
		log.Fatal(err)
	}

	// Set connection pool settings for both databases
	for _, d := range []*sql.DB{db, emailDB} {
		d.SetMaxOpenConns(1)
		d.SetMaxIdleConns(1)
		d.SetConnMaxLifetime(time.Hour)
	}

	// Set additional PRAGMAs for optimization for both databases
	for _, d := range []*sql.DB{db, emailDB} {
		_, err = d.Exec(`
			PRAGMA synchronous = NORMAL;
			PRAGMA journal_mode = WAL;
			PRAGMA busy_timeout = 5000;
			PRAGMA temp_store = MEMORY;
			PRAGMA mmap_size = 30000000000;
			PRAGMA cache_size = -2000;
			PRAGMA page_size = 4096;
			PRAGMA auto_vacuum = INCREMENTAL;
		`)
		if err != nil {
			log.Printf("Warning: Could not set all PRAGMAs: %v", err)
		}
	}

	// Create tables for both databases
	if err := createTable(); err != nil {
		log.Fatal(err)
	}

	if err := createEmailTable(); err != nil {
		log.Fatal(err)
	}

	// Check initial database sizes
	log.Printf("Checking initial database sizes")
	if err := checkDatabaseSize(); err != nil {
		log.Printf("Warning: Could not check initial database size: %v", err)
	}

	// Verify initial state
	log.Printf("Verifying initial table state")
	if err := verifyTableState(); err != nil {
		log.Printf("Warning: Could not verify initial table state: %v", err)
	}
}

func createTable() error {
	// Create content table with optimized settings
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS files_content (
			file TEXT,
			sheet TEXT,
			row INTEGER,
			content TEXT
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating content table: %v", err)
	}

	// Create FTS4 virtual table with optimized settings
	_, err = db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS files_fts USING fts4(
			file,
			sheet,
			row,
			content,
			tokenize=simple
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating FTS4 table: %v", err)
	}

	// Create trigger to maintain FTS4 table
	_, err = db.Exec(`
		DROP TRIGGER IF EXISTS files_ai;
		DROP TRIGGER IF EXISTS files_ad;
		DROP TRIGGER IF EXISTS files_au;
		
		CREATE TRIGGER files_ai AFTER INSERT ON files_content BEGIN
			INSERT INTO files_fts(file, sheet, row, content)
			VALUES (new.file, new.sheet, new.row, new.content);
		END;
		
		CREATE TRIGGER files_ad AFTER DELETE ON files_content BEGIN
			DELETE FROM files_fts 
			WHERE file = old.file AND sheet = old.sheet AND row = old.row;
		END;
		
		CREATE TRIGGER files_au AFTER UPDATE ON files_content BEGIN
			DELETE FROM files_fts 
			WHERE file = old.file AND sheet = old.sheet AND row = old.row;
			INSERT INTO files_fts(file, sheet, row, content)
			VALUES (new.file, new.sheet, new.row, new.content);
		END;
	`)
	if err != nil {
		return fmt.Errorf("error creating triggers: %v", err)
	}

	return nil
}

func createEmailTable() error {
	// Create email content table with optimized settings
	_, err := emailDB.Exec(`
		CREATE TABLE IF NOT EXISTS email_content (
			file TEXT,
			sheet TEXT,
			row INTEGER,
			email TEXT,
			content TEXT
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating email content table: %v", err)
	}

	// Create index on email column
	_, err = emailDB.Exec(`
		CREATE INDEX IF NOT EXISTS idx_email ON email_content(email)
	`)
	if err != nil {
		return fmt.Errorf("error creating email index: %v", err)
	}

	return nil
}

func verifyTableState() error {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM files_content").Scan(&count)
	if err != nil {
		return fmt.Errorf("error checking table state: %v", err)
	}
	log.Printf("Current number of rows in files_content: %d", count)
	return nil
}

func checkDatabaseSize() error {
	var size int64
	err := db.QueryRow("SELECT page_count * page_size as size FROM pragma_page_count(), pragma_page_size()").Scan(&size)
	if err != nil {
		return fmt.Errorf("error checking database size: %v", err)
	}
	log.Printf("Current database size: %.2f MB", float64(size)/1024/1024)
	return nil
}

func getDatabaseSize(emailOnly bool) (int64, error) {
	// Select database based on type
	database := db
	if emailOnly {
		database = emailDB
	}

	var size int64
	err := database.QueryRow("SELECT page_count * page_size as size FROM pragma_page_count(), pragma_page_size()").Scan(&size)
	if err != nil {
		return 0, fmt.Errorf("error getting database size: %v", err)
	}
	return size, nil
}

type ImportJob struct {
	Path      string
	Extension string
}

func verifyContentIndexing() error {
	// Get a sample row from the content table
	var content string
	err := db.QueryRow(`
		SELECT content 
		FROM files_content 
		LIMIT 1
	`).Scan(&content)
	if err != nil {
		return fmt.Errorf("error getting sample content: %v", err)
	}

	log.Printf("Sample content from database: %q", content)

	// Try to search for a word from the content
	words := strings.Fields(content)
	if len(words) > 0 {
		sampleWord := words[0]
		log.Printf("Attempting to search for sample word: %q", sampleWord)

		var count int
		err := db.QueryRow(`
			SELECT COUNT(*)
			FROM files_fts
			WHERE content MATCH ?
		`, "*"+sampleWord+"*").Scan(&count)
		if err != nil {
			return fmt.Errorf("error searching for sample word: %v", err)
		}
		log.Printf("Found %d matches for sample word %q", count, sampleWord)
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

	if len(rows) == 0 {
		return nil, fmt.Errorf("empty CSV file")
	}

	var documents []map[string]interface{}

	for rowNum, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}

		// Join all values with a separator
		content := strings.Join(row, " - ")

		doc := map[string]interface{}{
			"file":    path,
			"sheet":   "Sheet1",
			"row":     rowNum + 2,
			"content": content,
		}
		documents = append(documents, doc)
	}
	return documents, nil
}

func readExcelFile(path string) ([]map[string]interface{}, error) {
	// Check if it's an XLS file
	if strings.HasSuffix(strings.ToLower(path), ".xls") {
		// Open XLS file
		xlFile, err := xls.Open(path, "utf-8")
		if err != nil {
			return nil, fmt.Errorf("failed to open XLS file: %v", err)
		}

		var documents []map[string]interface{}
		rowNum := 1

		// Process each sheet
		for i := 0; i < xlFile.NumSheets(); i++ {
			sheet := xlFile.GetSheet(i)
			if sheet == nil {
				continue
			}

			// Get sheet name
			sheetName := sheet.Name
			if sheetName == "" {
				sheetName = fmt.Sprintf("Sheet%d", i+1)
			}

			// Process each row
			for rowIndex := 1; rowIndex < int(sheet.MaxRow); rowIndex++ {
				row := sheet.Row(rowIndex)
				if row == nil {
					continue
				}

				// Collect all cell values
				var colValues []string
				for colIndex := 0; colIndex < int(row.LastCol()); colIndex++ {
					cell := row.Col(colIndex)
					if cell != "" {
						colValues = append(colValues, cell)
					}
				}

				// Join all values with a separator
				content := strings.Join(colValues, " - ")

				doc := map[string]interface{}{
					"file":    path,
					"sheet":   sheetName,
					"row":     rowNum,
					"content": content,
				}
				documents = append(documents, doc)
				rowNum++
			}
		}

		return documents, nil
	}

	// Handle XLSX files
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open Excel file: %v", err)
	}
	defer f.Close()

	var documents []map[string]interface{}
	rowNum := 1

	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}

		if len(rows) == 0 {
			continue
		}

		for _, row := range rows[1:] {
			if len(row) == 0 {
				continue
			}

			// Join all values with a separator
			content := strings.Join(row, " - ")

			doc := map[string]interface{}{
				"file":    path,
				"sheet":   sheet,
				"row":     rowNum,
				"content": content,
			}
			documents = append(documents, doc)
			rowNum++
		}
	}
	return documents, nil
}

func resetDatabase() error {
	// Drop existing tables
	_, err := db.Exec(`
		DROP TABLE IF EXISTS files_content;
		DROP TABLE IF EXISTS files_fts;
	`)
	if err != nil {
		return fmt.Errorf("error dropping existing tables: %v", err)
	}

	// Recreate tables
	return createTable()
}

func resetEmailDatabase() error {
	// Drop existing tables
	_, err := emailDB.Exec(`
		DROP TABLE IF EXISTS email_content;
	`)
	if err != nil {
		return fmt.Errorf("error dropping existing email tables: %v", err)
	}

	// Recreate tables
	return createEmailTable()
}

func importToSQLite(files []string, extensions []string, resetDB bool, emailOnly bool) error {
	// Select database based on type
	database := db
	if emailOnly {
		database = emailDB
	}

	// Reset database if requested
	if resetDB {
		if emailOnly {
			if err := resetEmailDatabase(); err != nil {
				return fmt.Errorf("error resetting email database: %v", err)
			}
		} else {
			if err := resetDatabase(); err != nil {
				return fmt.Errorf("error resetting database: %v", err)
			}
		}
	}

	// Create a mutex for database access
	var dbMutex sync.Mutex

	// Create a channel for jobs with larger buffer
	jobs := make(chan ImportJob, 5000)
	results := make(chan error, 5000)
	rowCounts := make(chan int, 5000)

	// Increase number of workers for better parallelization
	numWorkers := runtime.NumCPU()

	// Start worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				var docs []map[string]interface{}
				var err error

				if job.Extension == "csv" {
					docs, err = readCSVFile(job.Path)
				} else {
					docs, err = readExcelFile(job.Path)
				}

				if err != nil {
					results <- fmt.Errorf("error reading file %s: %v", job.Path, err)
					rowCounts <- 0
					continue
				}

				// Lock database access
				dbMutex.Lock()

				// Begin transaction for this batch
				tx, err := database.Begin()
				if err != nil {
					dbMutex.Unlock()
					results <- fmt.Errorf("error starting transaction for %s: %v", job.Path, err)
					rowCounts <- 0
					continue
				}

				// Prepare statement based on database type
				var stmt *sql.Stmt
				if emailOnly {
					stmt, err = tx.Prepare(`
						INSERT INTO email_content (file, sheet, row, email, content)
						VALUES (?, ?, ?, ?, ?)
					`)
				} else {
					stmt, err = tx.Prepare(`
						INSERT INTO files_content (file, sheet, row, content)
						VALUES (?, ?, ?, ?)
					`)
				}

				if err != nil {
					tx.Rollback()
					dbMutex.Unlock()
					results <- fmt.Errorf("error preparing statement for %s: %v", job.Path, err)
					rowCounts <- 0
					continue
				}

				// Batch insert rows
				rowsInserted := 0
				insertError := false
				for _, doc := range docs {
					if emailOnly {
						// Extract email from content
						email := extractEmail(doc["content"].(string))
						if email != "" {
							_, err = stmt.Exec(
								doc["file"],
								doc["sheet"],
								doc["row"],
								email,
								doc["content"],
							)
						} else {
							continue // Skip rows without email
						}
					} else {
						_, err = stmt.Exec(
							doc["file"],
							doc["sheet"],
							doc["row"],
							doc["content"],
						)
					}

					if err != nil {
						log.Printf("Warning: Error inserting row for %s: %v", job.Path, err)
						insertError = true
						break
					}
					rowsInserted++
				}

				stmt.Close()
				if insertError {
					tx.Rollback()
					dbMutex.Unlock()
					results <- fmt.Errorf("error inserting data for %s: %v", job.Path, err)
					rowCounts <- 0
					continue
				}

				if err = tx.Commit(); err != nil {
					dbMutex.Unlock()
					results <- fmt.Errorf("error committing transaction for %s: %v", job.Path, err)
					rowCounts <- 0
					continue
				}

				// Unlock database access
				dbMutex.Unlock()
				results <- nil
				rowCounts <- rowsInserted
			}
		}()
	}

	// Send jobs to workers
	go func() {
		for _, file := range files {
			ext := filepath.Ext(file)
			if len(ext) > 0 {
				ext = ext[1:]
			}
			for _, allowedExt := range extensions {
				if ext == allowedExt {
					jobs <- ImportJob{
						Path:      file,
						Extension: ext,
					}
					break
				}
			}
		}
		close(jobs)
	}()

	// Wait for all workers to finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results and count rows
	var importErrors []error
	totalRows := 0
	totalFiles := 0
	failedFiles := 0

	for err := range results {
		rows := <-rowCounts
		totalFiles++
		if err != nil {
			failedFiles++
			importErrors = append(importErrors, err)
		} else {
			totalRows += rows
		}
	}

	if len(importErrors) > 0 {
		return fmt.Errorf("encountered %d errors during import: %v", len(importErrors), importErrors)
	}

	log.Printf("Import completed: %d rows imported from %d files (%d failed)", totalRows, totalFiles, failedFiles)

	return nil
}

func extractEmail(content string) string {
	// Simple email regex pattern
	emailRegex := regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	matches := emailRegex.FindString(content)
	return matches
}

func searchInSQLite(query string, page, pageSize int, emailOnly bool) ([]Match, int, error) {
	if query == "" {
		return nil, 0, fmt.Errorf("search query cannot be empty")
	}

	// Select database based on search type
	database := db
	if emailOnly {
		database = emailDB
	}

	var totalCount int
	var rows *sql.Rows
	var err error

	if emailOnly {
		// Search in email database
		err = database.QueryRow(`
			SELECT COUNT(*) 
			FROM email_content 
			WHERE email LIKE ?
		`, "%"+query+"%").Scan(&totalCount)
		if err != nil {
			return nil, 0, fmt.Errorf("database error getting count: %v", err)
		}

		offset := (page - 1) * pageSize
		rows, err = database.Query(`
			SELECT file, sheet, row, email, content
			FROM email_content
			WHERE email LIKE ?
			ORDER BY row
			LIMIT ? OFFSET ?
		`, "%"+query+"%", pageSize, offset)
	} else {
		// Search in main database
		err = database.QueryRow(`
			SELECT COUNT(*) 
			FROM files_content 
			WHERE content LIKE ?
		`, "%"+query+"%").Scan(&totalCount)
		if err != nil {
			return nil, 0, fmt.Errorf("database error getting count: %v", err)
		}

		offset := (page - 1) * pageSize
		rows, err = database.Query(`
			SELECT file, sheet, row, content
			FROM files_content
			WHERE content LIKE ?
			ORDER BY row
			LIMIT ? OFFSET ?
		`, "%"+query+"%", pageSize, offset)
	}

	if err != nil {
		return nil, 0, fmt.Errorf("database error: %v", err)
	}
	defer rows.Close()

	var matches []Match
	for rows.Next() {
		var match Match
		if emailOnly {
			err := rows.Scan(&match.File, &match.Sheet, &match.Row, &match.Email, &match.Content)
			if err != nil {
				return nil, 0, fmt.Errorf("error scanning results: %v", err)
			}
		} else {
			err := rows.Scan(&match.File, &match.Sheet, &match.Row, &match.Content)
			if err != nil {
				return nil, 0, fmt.Errorf("error scanning results: %v", err)
			}
		}
		matches = append(matches, match)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating results: %v", err)
	}

	return matches, totalCount, nil
}

func importHandler(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	log.Printf("Import request received at %v", startTime.Format(time.RFC3339))

	if r.Method != http.MethodPost {
		log.Printf("Invalid method: %s", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Invalid request body: %v", err)
		http.Error(w, "invalid request", 400)
		return
	}

	log.Printf("Import request: files=%v, extensions=%v, resetDB=%v, emailOnly=%v",
		req.Files, req.Extensions, req.ResetDB, req.EmailOnly)

	err := importToSQLite(req.Files, req.Extensions, req.ResetDB, req.EmailOnly)
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

	// Get the total rows from the appropriate database
	var totalRows int
	database := db
	if req.EmailOnly {
		database = emailDB
	}

	err = database.QueryRow("SELECT COUNT(*) FROM " + getTableName(req.EmailOnly)).Scan(&totalRows)
	if err != nil {
		log.Printf("Warning: Could not get total rows: %v", err)
		totalRows = 0
	}

	// Get the total number of unique files from the database
	var totalFiles int
	err = database.QueryRow("SELECT COUNT(DISTINCT file) FROM " + getTableName(req.EmailOnly)).Scan(&totalFiles)
	if err != nil {
		log.Printf("Warning: Could not get total files: %v", err)
		totalFiles = 0
	}

	resp := ImportResponse{
		Status:     "success",
		Message:    fmt.Sprintf("Data imported successfully in %v", duration),
		TotalRows:  totalRows,
		TotalFiles: totalFiles,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func getTableName(isEmailDB bool) string {
	if isEmailDB {
		return "email_content"
	}
	return "files_content"
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

	// Set default values for pagination
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 1 {
		req.PageSize = 10
	}

	log.Printf("Search request: query=%q, directories=%v, extensions=%v, page=%d, pageSize=%d, emailOnly=%v",
		req.Query, req.Directories, req.Extensions, req.Page, req.PageSize, req.EmailOnly)

	matches, totalCount, err := searchInSQLite(req.Query, req.Page, req.PageSize, req.EmailOnly)
	if err != nil {
		log.Printf("Search error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	duration := time.Since(startTime)
	log.Printf("Search completed in %v, found %d matches", duration, totalCount)

	totalPages := (totalCount + req.PageSize - 1) / req.PageSize
	resp := SearchResponse{
		Matches:     matches,
		TotalCount:  totalCount,
		TotalPages:  totalPages,
		CurrentPage: req.Page,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("Error encoding response: %v", err)
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		return
	}
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req StatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Select database based on emailOnly flag
	database := db
	if req.EmailOnly {
		database = emailDB
	}

	// Get total rows
	var totalRows int
	err := database.QueryRow("SELECT COUNT(*) FROM " + getTableName(req.EmailOnly)).Scan(&totalRows)
	if err != nil {
		log.Printf("Error getting total rows: %v", err)
		http.Error(w, "Error getting status", http.StatusInternalServerError)
		return
	}

	// Get database size
	dbSize, err := getDatabaseSize(req.EmailOnly)
	if err != nil {
		log.Printf("Error getting database size: %v", err)
		http.Error(w, "Error getting status", http.StatusInternalServerError)
		return
	}

	// Get last import time from log file
	lastImport := "Unknown"
	if logFile, err := os.Open("finder.log"); err == nil {
		defer logFile.Close()
		scanner := bufio.NewScanner(logFile)
		var lastLine string
		for scanner.Scan() {
			lastLine = scanner.Text()
		}
		if strings.Contains(lastLine, "Import completed") {
			lastImport = lastLine
		}
	}

	resp := StatusResponse{
		TotalRows:   totalRows,
		TotalFiles:  0, // This would need to be tracked separately if needed
		FailedFiles: 0, // This would need to be tracked separately if needed
		LastImport:  lastImport,
		DBSize:      fmt.Sprintf("%.2f MB", float64(dbSize)/1024/1024),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func checkImportedFiles(files []string, emailOnly bool) ([]string, []string, error) {
	database := db
	if emailOnly {
		database = emailDB
	}

	tableName := getTableName(emailOnly)

	var importedFiles []string
	var notImportedFiles []string

	for _, file := range files {
		var count int
		err := database.QueryRow(fmt.Sprintf(`
			SELECT COUNT(*) 
			FROM %s 
			WHERE file = ?
		`, tableName), file).Scan(&count)

		if err != nil {
			return nil, nil, fmt.Errorf("error checking file %s: %v", file, err)
		}

		if count > 0 {
			importedFiles = append(importedFiles, file)
		} else {
			notImportedFiles = append(notImportedFiles, file)
		}
	}

	return importedFiles, notImportedFiles, nil
}

func checkFilesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CheckFilesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Check in the appropriate database based on emailOnly flag
	importedFiles, notImportedFiles, err := checkImportedFiles(req.Files, req.EmailOnly)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := CheckFilesResponse{
		ImportedFiles:    importedFiles,
		NotImportedFiles: notImportedFiles,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	// Set up logging to file
	logFile, err := os.OpenFile("finder.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal("Failed to open log file:", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	log.Printf("Application starting...")

	svcConfig := &service.Config{
		Name:        "Finder",
		DisplayName: "Finder Service",
		Description: "A service for importing and searching files.",
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatal(err)
	}

	if len(os.Args) > 1 {
		log.Printf("Service command received: %s", os.Args[1])
		err = service.Control(s, os.Args[1])
		if err != nil {
			fmt.Printf("Valid actions: %q\n", service.ControlAction)
			log.Fatal(err)
		}
		return
	}

	log.Printf("Service starting...")
	err = s.Run()
	if err != nil {
		log.Fatal(err)
	}
}
