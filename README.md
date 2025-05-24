# Finder

A fast and efficient tool for searching through Excel (.xlsx, .xls) and CSV files. Built with Go for high performance and a simple web interface.

## Features

- Search through multiple Excel and CSV files simultaneously
- Support for multiple file extensions (.xlsx, .xls, .csv)
- Concurrent processing for faster search results
- Simple and intuitive web interface
- Real-time search results display
- Case-insensitive search
- Detailed match information including file, sheet, and row numbers

## Prerequisites

- Go 1.20 or higher
- Modern web browser

## Installation

1. Clone the repository:
```bash
git clone https://github.com/dotrungduchd/finder.git
cd finder
```

2. Install dependencies:
```bash
go mod download
```

## Usage

1. Start the server:
```bash
go run main.go
```

2. Open your web browser and navigate to:
```
http://localhost:8080/static/
```

3. In the web interface:
   - Enter the directory path(s) to search in
   - Specify file extensions to search (e.g., "xlsx, xls, csv")
   - Enter your search query
   - Click "Search" to start the search

## Search Results

Results are displayed in a table with the following information:
- File: The path of the file containing the match
- Sheet: The sheet name (for Excel files) or "Sheet1" (for CSV files)
- Row: The row number where the match was found
- Content: The matching row content, with cells separated by " - "

## Performance

- Uses concurrent processing for multiple files
- Optimized search algorithm for quick results
- Worker pool to manage system resources
- Efficient memory usage

## Technical Details

- Built with Go and the excelize library
- Uses goroutines for concurrent processing
- Implements a worker pool pattern
- Web interface built with vanilla JavaScript

## Contributing

Feel free to submit issues and enhancement requests!

## License

This project is licensed under the MIT License - see the LICENSE file for details. 