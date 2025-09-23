package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	HOST = "0.0.0.0"
	PORT = "80"
	TYPE = "tcp"
	LOG_DIR = "/var/log/gps-ingestor"
	LOG_FILE = "gps-ingestor.log"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

func (l LogLevel) String() string {
	return [...]string{"DEBUG", "INFO", "WARN", "ERROR"}[l]
}

type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	ClientIP  string `json:"client_ip,omitempty"`
	Filename  string `json:"filename,omitempty"`
	ByteCount int    `json:"byte_count,omitempty"`
	Error     string `json:"error,omitempty"`
}

var (
	logFile *os.File
	logger  *log.Logger
)

func initLogging() error {
	err := os.MkdirAll(LOG_DIR, 0755)
	if err != nil {
		return fmt.Errorf("failed to create log directory: %v", err)
	}

	logPath := filepath.Join(LOG_DIR, LOG_FILE)
	logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}

	logger = log.New(logFile, "", 0)
	return nil
}

func logMessage(level LogLevel, message string, clientIP, filename, errorMsg string, byteCount int) {
	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level.String(),
		Message:   message,
		ClientIP:  clientIP,
		Filename:  filename,
		ByteCount: byteCount,
		Error:     errorMsg,
	}

	jsonLog, _ := json.Marshal(entry)

	// Log to file (JSON format)
	if logger != nil {
		logger.Println(string(jsonLog))
	}

	// Also log to console for systemd
	log.Printf("[%s] %s", level.String(), message)
}

func main() {
	err := initLogging()
	if err != nil {
		log.Fatal("Failed to initialize logging:", err)
	}
	defer logFile.Close()

	listen, err := net.Listen(TYPE, HOST+":"+PORT)
	if err != nil {
		logMessage(ERROR, "Error listening", "", "", err.Error(), 0)
		log.Fatal("Error listening:", err.Error())
	}
	defer listen.Close()

	logMessage(INFO, fmt.Sprintf("GPS Data Ingestor TCP server listening on %s:%s", HOST, PORT), "", "", "", 0)

	for {
		conn, err := listen.Accept()
		if err != nil {
			logMessage(ERROR, "Error accepting connection", "", "", err.Error(), 0)
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	clientIP := conn.RemoteAddr().String()
	logMessage(INFO, "New connection established", clientIP, "", "", 0)

	// Set a shorter read timeout to detect when no more data is coming
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))

	// Read data in chunks
	buffer := make([]byte, 4096)
	var data []byte

	for {
		n, err := conn.Read(buffer)
		if n > 0 {
			data = append(data, buffer[:n]...)
			// Reset timeout when we receive data
			conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Timeout means no more data is coming, process what we have
				break
			}
			logMessage(ERROR, "Error reading data from connection", clientIP, "", err.Error(), 0)
			return
		}
	}

	if len(data) == 0 {
		logMessage(WARN, "No data received from connection", clientIP, "", "", 0)
		return
	}

	timestamp := time.Now().Unix()
	filename := fmt.Sprintf("data_%d.txt", timestamp)

	err := os.MkdirAll("data", 0755)
	if err != nil {
		logMessage(ERROR, "Error creating data directory", clientIP, "", err.Error(), 0)
		return
	}

	filepath := fmt.Sprintf("data/%s", filename)
	err = os.WriteFile(filepath, data, 0644)
	if err != nil {
		logMessage(ERROR, "Error writing data to file", clientIP, filename, err.Error(), len(data))
		return
	}

	logMessage(INFO, "Data successfully ingested and saved", clientIP, filename, "", len(data))

	response := fmt.Sprintf("Data ingested successfully\nFilename: %s\nTimestamp: %d\nBytes: %d\n", filename, timestamp, len(data))
	conn.Write([]byte(response))
}