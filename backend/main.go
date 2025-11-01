package main

import (
	"database/sql"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	_ "github.com/mattn/go-sqlite3"
	"github.com/google/uuid"
)

type Notification struct {
	ID          string    `json:"id"`
	Message     string    `json:"message"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	Device      string    `json:"device"`
	Status      string    `json:"status"` // "pending", "active", "completed"
	RepeatCount int       `json:"repeat_count"` // how many times to repeat TTS audio
}

type ChromecastDevice struct {
	Name     string `json:"name"`
	UUID     string `json:"uuid"`
	Address  string `json:"address"`
}

type App struct {
	DB                *sql.DB
	ActiveCasts       map[string]*CastSession
	CastMutex         sync.RWMutex
	VideoGenMutex     sync.Mutex  // Prevents concurrent video pre-generation
	VideoGenInProgress map[string]bool // Track which notifications are being generated
}

var appInstance *App

func main() {
	// Initialize database
	db, err := initDB()
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	appInstance = &App{
		DB:                db,
		ActiveCasts:       make(map[string]*CastSession),
		VideoGenInProgress: make(map[string]bool),
	}

	// Start the scheduler
	go appInstance.startScheduler()

	// Start device discovery in background
	go appInstance.startDeviceDiscovery()

	// Setup Fiber app
	app := fiber.New(fiber.Config{
		AppName: "Notification Service",
	})

	// CORS middleware
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,PUT,DELETE,OPTIONS",
		AllowHeaders: "Origin,Content-Type,Accept,Authorization",
	}))

	// Routes
	api := app.Group("/api")
	api.Get("/devices", getDevices)
	api.Post("/notifications", createNotification)
	api.Get("/notifications", getNotifications)
	api.Get("/notifications/:id", getNotification)
	api.Delete("/notifications/:id", deleteNotification)

	// Route to serve notification content for Chromecast (HTML - legacy)
	app.Get("/notification/:id", serveNotificationContent)
	
	// Route to serve notification images for Chromecast
	app.Get("/notification-image/:id", serveNotificationImage)
	
	// Route to serve notification videos for Chromecast (HLS format)
	app.Get("/notification-video/:id/*", serveNotificationVideo)

	// Serve frontend static files if needed
	app.Static("/", "./static")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s", port)
	if err := app.Listen(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func initDB() (*sql.DB, error) {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/notifications.db"
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll("/data", 0755); err != nil {
		log.Printf("Warning: Could not create /data directory: %v", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, err
	}

	// Create table
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS notifications (
		id TEXT PRIMARY KEY,
		message TEXT NOT NULL,
		start_time DATETIME NOT NULL,
		end_time DATETIME NOT NULL,
		device TEXT NOT NULL,
		status TEXT DEFAULT 'pending',
		repeat_count INTEGER DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := db.Exec(createTableSQL); err != nil {
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	return db, nil
}

// Helper function to parse time in multiple formats (RFC3339 or custom format)
func parseTimeInUTC(timeStr string) (time.Time, error) {
	// Try RFC3339 format first (ISO 8601 with 'T' separator)
	if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
		return t.UTC(), nil
	}
	// Try RFC3339 without timezone (with 'Z' suffix)
	if t, err := time.Parse("2006-01-02T15:04:05Z", timeStr); err == nil {
		return t.UTC(), nil
	}
	// Try custom format (space separator, no timezone)
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", timeStr, time.UTC); err == nil {
		return t, nil
	}
	// If all fail, return the first error
	return time.Parse(time.RFC3339, timeStr)
}

// API Handlers
func getDevices(c *fiber.Ctx) error {
	devices := appInstance.discoverDevices()
	return c.JSON(devices)
}

func createNotification(c *fiber.Ctx) error {
	var requestBody struct {
		Message     string `json:"message"`
		Device      string `json:"device"`
		StartTime   string `json:"start_time"`
		EndTime     string `json:"end_time"`
		RepeatCount int    `json:"repeat_count"`
	}
	
	if err := c.BodyParser(&requestBody); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	// Parse ISO 8601 timestamps
	startTime, err := time.Parse(time.RFC3339, requestBody.StartTime)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fmt.Sprintf("Invalid start_time format: %v", err)})
	}
	
	endTime, err := time.Parse(time.RFC3339, requestBody.EndTime)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fmt.Sprintf("Invalid end_time format: %v", err)})
	}

	// Default repeat count to 1 if not provided or invalid
	repeatCount := requestBody.RepeatCount
	if repeatCount < 1 {
		repeatCount = 1
	}
	
	notif := Notification{
		ID:          uuid.New().String(),
		Message:     requestBody.Message,
		Device:      requestBody.Device,
		StartTime:   startTime,
		EndTime:     endTime,
		Status:      "pending",
		RepeatCount: repeatCount,
	}

	// Insert into database
	// Convert to UTC for storage
	startTimeUTC := notif.StartTime.UTC()
	endTimeUTC := notif.EndTime.UTC()
	
	stmt, err := appInstance.DB.Prepare(`
		INSERT INTO notifications (id, message, start_time, end_time, device, status, repeat_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Database error"})
	}
	defer stmt.Close()

	_, err = stmt.Exec(
		notif.ID,
		notif.Message,
		startTimeUTC.Format("2006-01-02 15:04:05"),
		endTimeUTC.Format("2006-01-02 15:04:05"),
		notif.Device,
		notif.Status,
		notif.RepeatCount,
	)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to create notification"})
	}

	return c.Status(201).JSON(notif)
}

func getNotifications(c *fiber.Ctx) error {
	rows, err := appInstance.DB.Query(`
		SELECT id, message, start_time, end_time, device, status, repeat_count
		FROM notifications
		ORDER BY created_at DESC
	`)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Database error"})
	}
	defer rows.Close()

	var notifications []Notification
	for rows.Next() {
		var notif Notification
		var startTimeStr, endTimeStr string
		err := rows.Scan(
			&notif.ID,
			&notif.Message,
			&startTimeStr,
			&endTimeStr,
			&notif.Device,
			&notif.Status,
			&notif.RepeatCount,
		)
		if err != nil {
			continue
		}

		// Parse as UTC time (handles multiple formats)
		startTime, err := parseTimeInUTC(startTimeStr)
		if err != nil {
			log.Printf("Error parsing start_time: %v", err)
			continue
		}
		notif.StartTime = startTime
		
		endTime, err := parseTimeInUTC(endTimeStr)
		if err != nil {
			log.Printf("Error parsing end_time: %v", err)
			continue
		}
		notif.EndTime = endTime
		
		notifications = append(notifications, notif)
	}

	return c.JSON(notifications)
}

func getNotification(c *fiber.Ctx) error {
	id := c.Params("id")
	var notif Notification
	var startTimeStr, endTimeStr string

	err := appInstance.DB.QueryRow(`
		SELECT id, message, start_time, end_time, device, status, repeat_count
		FROM notifications
		WHERE id = ?
	`, id).Scan(
		&notif.ID,
		&notif.Message,
		&startTimeStr,
		&endTimeStr,
		&notif.Device,
		&notif.Status,
		&notif.RepeatCount,
	)

	if err == sql.ErrNoRows {
		return c.Status(404).JSON(fiber.Map{"error": "Notification not found"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Database error"})
	}

	// Parse as UTC time (handles multiple formats)
	startTime, err := parseTimeInUTC(startTimeStr)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Error parsing start_time: %v", err)})
	}
	notif.StartTime = startTime
	
	endTime, err := parseTimeInUTC(endTimeStr)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Error parsing end_time: %v", err)})
	}
	notif.EndTime = endTime

	return c.JSON(notif)
}

func deleteNotification(c *fiber.Ctx) error {
	id := c.Params("id")

	// Stop cast if active
	appInstance.stopCast(id)

	// Delete from database
	_, err := appInstance.DB.Exec("DELETE FROM notifications WHERE id = ?", id)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to delete notification"})
	}

	return c.JSON(fiber.Map{"message": "Notification deleted"})
}

func serveNotificationContent(c *fiber.Ctx) error {
	id := c.Params("id")
	var notif Notification
	var startTimeStr, endTimeStr string

	err := appInstance.DB.QueryRow(`
		SELECT id, message, start_time, end_time, device, status, repeat_count
		FROM notifications
		WHERE id = ?
	`, id).Scan(
		&notif.ID,
		&notif.Message,
		&startTimeStr,
		&endTimeStr,
		&notif.Device,
		&notif.Status,
		&notif.RepeatCount,
	)

	if err == sql.ErrNoRows {
		return c.Status(404).SendString("Notification not found")
	}
	if err != nil {
		return c.Status(500).SendString("Database error")
	}

	// Return HTML content for Chromecast to display
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Notification</title>
	<style>
		body {
			margin: 0;
			padding: 0;
			display: flex;
			justify-content: center;
			align-items: center;
			height: 100vh;
			background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
			font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
		}
		.message {
			text-align: center;
			color: white;
			font-size: 4em;
			padding: 40px;
			text-shadow: 2px 2px 4px rgba(0,0,0,0.3);
			word-wrap: break-word;
			max-width: 90%%;
		}
	</style>
</head>
<body>
	<div class="message">%s</div>
</body>
</html>`, html.EscapeString(notif.Message))

	c.Set("Content-Type", "text/html")
	return c.SendString(html)
}

func serveNotificationImage(c *fiber.Ctx) error {
	id := c.Params("id")
	var notif Notification
	var startTimeStr, endTimeStr string

	err := appInstance.DB.QueryRow(`
		SELECT id, message, start_time, end_time, device, status, repeat_count
		FROM notifications
		WHERE id = ?
	`, id).Scan(
		&notif.ID,
		&notif.Message,
		&startTimeStr,
		&endTimeStr,
		&notif.Device,
		&notif.Status,
		&notif.RepeatCount,
	)

	if err == sql.ErrNoRows {
		return c.Status(404).JSON(fiber.Map{"error": "Notification not found"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Database error"})
	}

	// Parse times
	startTime, err := parseTimeInUTC(startTimeStr)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to parse start_time"})
	}
	endTime, err := parseTimeInUTC(endTimeStr)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to parse end_time"})
	}
	notif.StartTime = startTime
	notif.EndTime = endTime

	// Generate or retrieve image with times
	imagePath, err := generateNotificationImageSimple(notif.Message, notif.ID, notif.StartTime, notif.EndTime)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to generate image: %v", err)})
	}

	// Read and serve the image file directly
	imageFile, err := os.Open(imagePath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to read image"})
	}
	defer imageFile.Close()

	// Get file info for content length
	fileInfo, err := imageFile.Stat()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to get file info"})
	}

	// Set content type and serve image
	c.Set("Content-Type", "image/png")
	
	// Send the file stream
	return c.SendStream(imageFile, int(fileInfo.Size()))
}

func serveNotificationVideo(c *fiber.Ctx) error {
	// Handle OPTIONS request for CORS (matching gochromecast example)
	if c.Method() == "OPTIONS" {
		c.Set("Access-Control-Allow-Origin", "*")
		c.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, PUT, OPTIONS, HEAD")
		c.Set("Access-Control-Allow-Headers", "Authorization, Origin, X-Requested-With, Content-Type, Accept, ngrok-skip-browser-warning")
		return c.SendStatus(204)
	}
	
	id := c.Params("id")
	filePath := c.Params("*") // The rest of the path (e.g., "playlist.m3u8" or "segment001.ts")
	
	// Build the full path to the requested file
	requestedPath := filepath.Join("./data/chunks", id, filePath)
	
	// Security check: ensure we're only serving files from the notification's directory
	if !strings.HasPrefix(requestedPath, filepath.Join("./data/chunks", id)) {
		return c.Status(403).JSON(fiber.Map{"error": "Invalid path"})
	}
	
	// Check if it's the playlist or a segment
	if filePath == "playlist.m3u8" || filePath == "" {
		// If no file specified or it's the playlist, we might need to generate it
		// First check if directory exists
		videoDir := filepath.Join("./data/chunks", id)
		playlistPath := filepath.Join(videoDir, "playlist.m3u8")
		
		if _, err := os.Stat(playlistPath); err != nil {
			// Playlist doesn't exist, need to generate video
			var notif Notification
			var startTimeStr, endTimeStr string
			
			err := appInstance.DB.QueryRow(`
				SELECT id, message, start_time, end_time, device, status, repeat_count
				FROM notifications
				WHERE id = ?
			`, id).Scan(
				&notif.ID,
				&notif.Message,
				&startTimeStr,
				&endTimeStr,
			&notif.Device,
			&notif.Status,
			&notif.RepeatCount,
		)
			
			if err == sql.ErrNoRows {
				return c.Status(404).JSON(fiber.Map{"error": "Notification not found"})
			}
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"error": "Database error"})
			}
			
			// Calculate video duration from start and end times
			startTime, err := parseTimeInUTC(startTimeStr)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"error": "Failed to parse start_time"})
			}
			endTime, err := parseTimeInUTC(endTimeStr)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"error": "Failed to parse end_time"})
			}
			notif.StartTime = startTime
			notif.EndTime = endTime
			
			// Generate image first with times
			imagePath, err := generateNotificationImageSimple(notif.Message, notif.ID, notif.StartTime, notif.EndTime)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to generate image: %v", err)})
			}
			
			duration := int(endTime.Sub(startTime).Seconds())
			if duration < 1 {
				duration = 10
			}
			
			// Convert end time to EST for TTS
			estLocation, err := time.LoadLocation("America/New_York")
			if err != nil {
				log.Printf("Warning: Could not load EST timezone for TTS, using UTC: %v", err)
				estLocation = time.UTC
			}
			endTimeEST := notif.EndTime.In(estLocation)
			
			// Generate TTS audio: "Michel is in the meeting until [end_time]"
			ttsText := fmt.Sprintf("Hi Dan, this message is to tell you that Michel is in a meeting until %s and he had this message for you: %s", endTimeEST.Format("3:04 PM"), notif.Message)
			audioPath, err := generateTTSAudio(ttsText, notif.ID, notif.RepeatCount)
			if err != nil {
				log.Printf("Failed to generate TTS audio for notification %s: %v (continuing without audio)", notif.ID, err)
				audioPath = "" // Continue without audio if TTS fails
			}
			
			// Generate HLS video with audio
			_, err = generateNotificationVideo(imagePath, notif.ID, duration, audioPath)
			if err != nil {
				log.Printf("Error generating video: %v", err)
				return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to generate video: %v", err)})
			}
		}
		
		// Serve the playlist
		requestedPath = playlistPath
	}
	
	// Determine content type based on file extension
	// Chromecast requires specific headers for HLS playback
	// Use application/x-mpegurl (not vnd.apple.mpegurl) to match gochromecast example
	if strings.HasSuffix(filePath, ".m3u8") {
		c.Set("Content-Type", "application/x-mpegurl")
		c.Set("Cache-Control", "no-cache")
		c.Set("Access-Control-Allow-Origin", "*")
		c.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, PUT, OPTIONS, HEAD")
		c.Set("Access-Control-Allow-Headers", "Authorization, Origin, X-Requested-With, Content-Type, Accept, ngrok-skip-browser-warning")
	} else if strings.HasSuffix(filePath, ".ts") {
		c.Set("Content-Type", "video/mp2t")
		c.Set("Cache-Control", "public, max-age=3600")
		c.Set("Access-Control-Allow-Origin", "*")
		c.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, PUT, OPTIONS, HEAD")
		c.Set("Access-Control-Allow-Headers", "Authorization, Origin, X-Requested-With, Content-Type, Accept, ngrok-skip-browser-warning")
	}
	
	// Serve the file
	if _, err := os.Stat(requestedPath); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "File not found"})
	}
	
	return c.SendFile(requestedPath)
}

