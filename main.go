package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	HOST = "0.0.0.0"
	PORT = "80"
	TYPE = "tcp"
	LOG_DIR = "/var/log/gps-ingestor"
	LOG_FILE = "gps-ingestor.log"
	MAX_WORKERS = 100
	MAX_CONNECTIONS = 1000
	WORKER_BUFFER_SIZE = 100
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
	BinaryDataHex string `json:"binary_data_hex,omitempty"`
	BinaryDataString string `json:"binary_data_string,omitempty"`
}

type ConnectionJob struct {
	conn net.Conn
	id   uint64
}

type ServerMetrics struct {
	activeConnections   int64
	totalConnections    int64
	processedRequests   int64
	totalBytesProcessed int64
}

var (
	logFile *os.File
	logger  *log.Logger
	metrics ServerMetrics

	// Concurrency control
	connectionSemaphore chan struct{}
	workerPool          chan ConnectionJob
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
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
	logMessageWithBinary(level, message, clientIP, filename, errorMsg, byteCount, nil)
}

func logMessageWithBinary(level LogLevel, message string, clientIP, filename, errorMsg string, byteCount int, binaryData []byte) {
	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level.String(),
		Message:   message,
		ClientIP:  clientIP,
		Filename:  filename,
		ByteCount: byteCount,
		Error:     errorMsg,
	}

	if binaryData != nil && len(binaryData) > 0 {
		entry.BinaryDataHex = fmt.Sprintf("%x", binaryData)
		entry.BinaryDataString = fmt.Sprintf("%q", string(binaryData))
	}

	jsonLog, _ := json.Marshal(entry)

	// Log to file (JSON format)
	if logger != nil {
		logger.Println(string(jsonLog))
	}

	// Also log to console for systemd
	log.Printf("[%s] %s", level.String(), message)
}

func initConcurrency() {
	ctx, cancel = context.WithCancel(context.Background())
	connectionSemaphore = make(chan struct{}, MAX_CONNECTIONS)
	workerPool = make(chan ConnectionJob, WORKER_BUFFER_SIZE)

	// Start worker goroutines
	for i := 0; i < MAX_WORKERS; i++ {
		wg.Add(1)
		go worker(i)
	}

	logMessage(INFO, fmt.Sprintf("Started %d worker goroutines with max %d connections", MAX_WORKERS, MAX_CONNECTIONS), "", "", "", 0)

	// Start metrics reporting goroutine
	wg.Add(1)
	go metricsReporter()
}

func metricsReporter() {
	defer wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			active := atomic.LoadInt64(&metrics.activeConnections)
			total := atomic.LoadInt64(&metrics.totalConnections)
			requests := atomic.LoadInt64(&metrics.processedRequests)
			bytes := atomic.LoadInt64(&metrics.totalBytesProcessed)

			logMessage(INFO, fmt.Sprintf("Metrics: Active=%d, Total=%d, Requests=%d, Bytes=%d", active, total, requests, bytes), "", "", "", 0)
		case <-ctx.Done():
			// Final metrics report
			active := atomic.LoadInt64(&metrics.activeConnections)
			total := atomic.LoadInt64(&metrics.totalConnections)
			requests := atomic.LoadInt64(&metrics.processedRequests)
			bytes := atomic.LoadInt64(&metrics.totalBytesProcessed)

			logMessage(INFO, fmt.Sprintf("Final metrics: Active=%d, Total=%d, Requests=%d, Bytes=%d", active, total, requests, bytes), "", "", "", 0)
			return
		}
	}
}

func worker(id int) {
	defer wg.Done()

	for {
		select {
		case job := <-workerPool:
			logMessage(DEBUG, fmt.Sprintf("Worker %d processing connection %d", id, job.id), "", "", "", 0)
			handleConnection(job.conn, job.id)
			<-connectionSemaphore // Release connection slot
			atomic.AddInt64(&metrics.activeConnections, -1)
		case <-ctx.Done():
			logMessage(INFO, fmt.Sprintf("Worker %d shutting down", id), "", "", "", 0)
			return
		}
	}
}

func main() {
	err := initLogging()
	if err != nil {
		log.Fatal("Failed to initialize logging:", err)
	}
	defer logFile.Close()

	initConcurrency()
	defer cancel()

	listen, err := net.Listen(TYPE, HOST+":"+PORT)
	if err != nil {
		logMessage(ERROR, "Error listening", "", "", err.Error(), 0)
		log.Fatal("Error listening:", err.Error())
	}
	defer listen.Close()

	logMessage(INFO, fmt.Sprintf("GPS Data Ingestor TCP server listening on %s:%s", HOST, PORT), "", "", "", 0)

	// Handle graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		logMessage(INFO, "Shutdown signal received, gracefully shutting down", "", "", "", 0)
		cancel()
		listen.Close()
	}()

	var connectionID uint64
	for {
		conn, err := listen.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				logMessage(INFO, "Server shutting down", "", "", "", 0)
				break
			default:
				logMessage(ERROR, "Error accepting connection", "", "", err.Error(), 0)
				continue
			}
			break
		}

		currentConnID := atomic.AddUint64(&connectionID, 1)
		atomic.AddInt64(&metrics.totalConnections, 1)

		select {
		case connectionSemaphore <- struct{}{}:
			atomic.AddInt64(&metrics.activeConnections, 1)
			select {
			case workerPool <- ConnectionJob{conn: conn, id: currentConnID}:
				// Job submitted successfully
			default:
				// Worker pool full, handle directly
				logMessage(WARN, "Worker pool full, handling connection directly", "", "", "", 0)
				go func(c net.Conn, id uint64) {
					handleConnection(c, id)
					<-connectionSemaphore
					atomic.AddInt64(&metrics.activeConnections, -1)
				}(conn, currentConnID)
			}
		default:
			// Connection limit reached
			logMessage(WARN, "Connection limit reached, rejecting connection", "", "", "", 0)
			conn.Close()
		}
	}

	// Wait for all workers to finish
	logMessage(INFO, "Waiting for workers to finish", "", "", "", 0)
	wg.Wait()
	logMessage(INFO, "Server stopped gracefully", "", "", "", 0)
}

func handleConnection(conn net.Conn, connectionID uint64) {
	defer conn.Close()

	clientIP := conn.RemoteAddr().String()
	logMessage(INFO, fmt.Sprintf("New connection established (ID: %d)", connectionID), clientIP, "", "", 0)

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

	// Print received binary data to console
	fmt.Printf("=== Connection %d - Received Data ===\n", connectionID)
	fmt.Printf("Client: %s\n", clientIP)
	fmt.Printf("Size: %d bytes\n", len(data))
	fmt.Printf("Raw data (hex): %x\n", data)
	fmt.Printf("Raw data (string): %q\n", string(data))
	fmt.Printf("=====================================\n")

	// Update metrics
	atomic.AddInt64(&metrics.processedRequests, 1)
	atomic.AddInt64(&metrics.totalBytesProcessed, int64(len(data)))

	logMessageWithBinary(INFO, fmt.Sprintf("Data successfully processed (ID: %d)", connectionID), clientIP, "", "", len(data), data)

	response := fmt.Sprintf("Data processed successfully\nBytes: %d\nConnection ID: %d\n", len(data), connectionID)
	conn.Write([]byte(response))
}