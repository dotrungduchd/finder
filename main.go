package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xuri/excelize/v2"
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

// SearchResult holds the results from a single file search
type SearchResult struct {
	Matches []Match
	Error   error
}

func searchExcelFiles(dirs []string, query string, extensions []string) ([]Match, error) {
	var allMatches []Match
	var wg sync.WaitGroup
	resultsChan := make(chan SearchResult, 100) // Buffered channel
	query = strings.ToLower(query)

	// Create a worker pool
	maxWorkers := 8 // Increased number of workers
	semaphore := make(chan struct{}, maxWorkers)

	// Process each directory
	for _, dir := range dirs {
		absPath, err := filepath.Abs(dir)
		if err != nil {
			fmt.Printf("Error getting absolute path for %s: %v\n", dir, err)
			continue
		}

		err = filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
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
						wg.Add(1)
						semaphore <- struct{}{} // Acquire semaphore
						go func(filePath string) {
							defer wg.Done()
							defer func() { <-semaphore }() // Release semaphore

							var fileMatches []Match
							var err error
							fmt.Println("Processing file:", filePath)
							if ext == "csv" {
								fileMatches, err = searchInCSV(filePath, query)
							} else {
								fileMatches, err = searchInExcel(filePath, query)
							}
							if err != nil {
								fmt.Printf("Error processing %s: %v\n", filePath, err)
							}
							resultsChan <- SearchResult{Matches: fileMatches, Error: err}
						}(path)
						break
					}
				}
			}
			return nil
		})

		if err != nil {
			fmt.Printf("Error walking directory %s: %v\n", absPath, err)
		}
	}

	// Close results channel when all workers are done
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	for result := range resultsChan {
		if result.Error == nil {
			allMatches = append(allMatches, result.Matches...)
		}
	}

	return allMatches, nil
}

func searchInCSV(path, query string) ([]Match, error) {
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

	var matches []Match
	for i, row := range rows {
		if len(row) == 0 {
			continue
		}

		// Check each cell for the query
		for _, cell := range row {
			if strings.Contains(strings.ToLower(cell), query) {
				joined := strings.Join(row, " - ")
				fmt.Printf("CSV Match in %s at row %d: %s\n", path, i+1, joined)
				matches = append(matches, Match{
					File:    path,
					Sheet:   "Sheet1",
					Row:     i + 1,
					Content: joined,
				})
				break // Found a match in this row, no need to check other cells
			}
		}
	}
	return matches, nil
}

func searchInExcel(path, query string) ([]Match, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var matches []Match
	sheets := f.GetSheetList()

	// Process each sheet
	for _, sheet := range sheets {
		rows, err := f.GetRows(sheet)
		if err != nil {
			fmt.Printf("Error reading sheet %s: %v\n", sheet, err)
			continue
		}

		for i, row := range rows {
			if len(row) == 0 {
				continue
			}

			// Check each cell for the query
			for _, cell := range row {
				if strings.Contains(strings.ToLower(cell), query) {
					joined := strings.Join(row, " - ")
					fmt.Printf("Excel Match in %s sheet %s at row %d: %s\n", path, sheet, i+1, joined)
					matches = append(matches, Match{
						File:    path,
						Sheet:   sheet,
						Row:     i + 1,
						Content: joined,
					})
					break // Found a match in this row, no need to check other cells
				}
			}
		}
	}

	return matches, nil
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}

	startTime := time.Now()
	fmt.Printf("Starting search for '%s' in directories: %v with extensions: %v\n",
		req.Query, req.Directories, req.Extensions)

	matches, _ := searchExcelFiles(req.Directories, req.Query, req.Extensions)

	duration := time.Since(startTime)
	fmt.Printf("Search completed in %v with %d matches\n", duration, len(matches))

	resp := SearchResponse{Matches: matches}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	http.HandleFunc("/search", searchHandler)
	fmt.Println("Server running at http://localhost:8080/static/")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
