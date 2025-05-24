# Excel File Finder

A service application for searching and importing Excel/CSV files. The application provides a web interface to search through Excel and CSV files in specified directories.

## Features

- Search through Excel (.xlsx, .xls) and CSV files
- Web-based user interface
- Windows service support for automatic startup
- Full-text search capabilities
- Support for multiple directories
- Real-time search results

## Prerequisites

- Go 1.16 or higher
- SQLite3
- Windows OS (for service installation)

## Installation

### Building from Source

1. Clone the repository:
```bash
git clone <repository-url>
cd finder
```

2. Install dependencies:
```bash
go mod download
```

3. Build the application:
```bash
# For Windows
GOOS=windows GOARCH=amd64 go build -o dist/finder.exe

# For macOS/Linux
go build -o finder
```

### Windows Service Installation

1. Create a directory for the application (e.g., `C:\Finder`)
2. Copy the following files to the directory:
   - `finder.exe`
   - `static` folder (containing index.html)
   - `finder.db` (if it exists)

3. Open Command Prompt as Administrator and navigate to the application directory:
```cmd
cd C:\Finder
```

4. Install the service:
```cmd
finder.exe install
```

5. Start the service:
```cmd
finder.exe start
```

## Service Management

The following commands are available for managing the Windows service:

- `finder.exe start` - Start the service
- `finder.exe stop` - Stop the service
- `finder.exe status` - Check service status
- `finder.exe uninstall` - Remove the service

## Usage

1. Access the web interface at http://localhost:8080/static/
2. Use the interface to:
   - Import Excel/CSV files from specified directories
   - Search through imported files
   - View search results with file, sheet, and row information

## API Endpoints

### Search
- **URL**: `/search`
- **Method**: `POST`
- **Request Body**:
```json
{
    "directories": ["path/to/directory"],
    "query": "search term",
    "extensions": ["xlsx", "xls", "csv"]
}
```

### Import
- **URL**: `/import`
- **Method**: `POST`
- **Request Body**:
```json
{
    "directories": ["path/to/directory"],
    "extensions": ["xlsx", "xls", "csv"]
}
```

## Troubleshooting

1. If the service fails to start:
   - Check Windows Event Viewer for error messages
   - Verify the application directory has proper permissions
   - Ensure port 8080 is not in use by another application

2. If the web interface is not accessible:
   - Verify the service is running
   - Check if port 8080 is accessible
   - Clear browser cache and try again

## Development

### Project Structure
```
finder/
├── main.go          # Main application code
├── static/          # Static web files
│   └── index.html   # Web interface
└── finder.db        # SQLite database
```

### Building for Development
```bash
go run main.go
```

## License

[dotrungduchd]

## Contributing

[TDB] 