package main

import (
	"fmt"
	"log"
	"os"
	"time"
)

func (a *App) startScheduler() {
	ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds
	defer ticker.Stop()

	for range ticker.C {
		a.checkAndProcessNotifications()
	}
}

func (a *App) checkAndProcessNotifications() {
	now := time.Now().UTC()

	// Pre-generate videos for notifications starting soon (within next 5 minutes)
	// Run in goroutine to avoid blocking the scheduler
	go a.preGenerateVideosForPendingNotifications(now)

	// Get pending notifications that should start (and haven't ended yet)
	rows, err := a.DB.Query(`
		SELECT id, message, start_time, end_time, device, status, repeat_count
		FROM notifications
		WHERE status = 'pending' 
		AND start_time <= ? 
		AND end_time > ?
	`, now.Format("2006-01-02 15:04:05"), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		log.Printf("Error querying pending notifications: %v", err)
		return
	}
	defer rows.Close()

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
			log.Printf("Error scanning notification row: %v", err)
			continue
		}

		// Parse as UTC time (handles multiple formats)
		startTime, err := parseTimeInUTC(startTimeStr)
		if err != nil {
			log.Printf("Error parsing start_time '%s': %v", startTimeStr, err)
			continue
		}
		notif.StartTime = startTime
		
		endTime, err := parseTimeInUTC(endTimeStr)
		if err != nil {
			log.Printf("Error parsing end_time '%s': %v", endTimeStr, err)
			continue
		}
		notif.EndTime = endTime

		log.Printf("[SCHEDULER DEBUG] Found pending notification %s: start=%v, end=%v, now=%v", notif.ID, startTime, endTime, now)

		// Start cast if it's time (use >= for start time to catch exact matches)
		if (now.After(notif.StartTime) || now.Equal(notif.StartTime)) && now.Before(notif.EndTime) {
			// Check if video is ready before casting
			playlistPath := fmt.Sprintf("./data/chunks/%s/playlist.m3u8", notif.ID)
			if _, err := os.Stat(playlistPath); err != nil {
				log.Printf("[SCHEDULER] Video not ready yet for notification %s, will retry in 10 seconds", notif.ID)
				continue
			}
			
			log.Printf("[SCHEDULER] Starting cast for notification %s", notif.ID)
			if err := a.startCast(notif.ID, notif.Device, notif.Message); err != nil {
				log.Printf("Failed to start cast for notification %s: %v", notif.ID, err)
			}
		} else {
			log.Printf("[SCHEDULER DEBUG] Skipping notification %s: not in time window", notif.ID)
		}
	}

	// Get active notifications that should end
	rows, err = a.DB.Query(`
		SELECT id, message, start_time, end_time, device, status, repeat_count
		FROM notifications
		WHERE status = 'active' AND end_time <= ?
	`, now.Format("2006-01-02 15:04:05"))
	if err != nil {
		log.Printf("Error querying active notifications: %v", err)
		return
	}
	defer rows.Close()

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
			log.Printf("Error scanning active notification row: %v", err)
			continue
		}

		// Parse as UTC time (handles multiple formats)
		endTime, err := parseTimeInUTC(endTimeStr)
		if err != nil {
			log.Printf("Error parsing end_time '%s': %v", endTimeStr, err)
			continue
		}
		notif.EndTime = endTime

		log.Printf("[SCHEDULER DEBUG] Found active notification %s: end=%v, now=%v", notif.ID, endTime, now)

		// Stop cast if end time reached (use >= to catch exact matches)
		if now.After(notif.EndTime) || now.Equal(notif.EndTime) {
			log.Printf("[SCHEDULER] Stopping cast for notification %s", notif.ID)
			if err := a.stopCast(notif.ID); err != nil {
				log.Printf("Failed to stop cast for notification %s: %v", notif.ID, err)
			}
		} else {
			log.Printf("[SCHEDULER DEBUG] Not stopping notification %s yet: end time not reached", notif.ID)
		}
	}
}

// preGenerateVideosForPendingNotifications generates videos for pending notifications
// that will start within the next 5 minutes, so they're ready when needed
func (a *App) preGenerateVideosForPendingNotifications(now time.Time) {
	// Recover from any panics to prevent crashing the entire app
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ERROR: Panic in preGenerateVideosForPendingNotifications: %v", r)
		}
	}()
	
	// Look for pending notifications starting within next 5 minutes
	futureTime := now.Add(5 * time.Minute)
	
	rows, err := a.DB.Query(`
		SELECT id, message, start_time, end_time, device, status, repeat_count
		FROM notifications
		WHERE status = 'pending' 
		AND start_time > ? 
		AND start_time <= ?
	`, now.Format("2006-01-02 15:04:05"), futureTime.Format("2006-01-02 15:04:05"))
	if err != nil {
		log.Printf("Error querying pending notifications for pre-generation: %v", err)
		return
	}
	defer rows.Close()

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

		// Parse times
		startTime, err := parseTimeInUTC(startTimeStr)
		if err != nil {
			continue
		}
		endTime, err := parseTimeInUTC(endTimeStr)
		if err != nil {
			continue
		}
		notif.StartTime = startTime
		notif.EndTime = endTime

		// Check if video already exists (HLS playlist)
		playlistPath := fmt.Sprintf("./data/chunks/%s/playlist.m3u8", notif.ID)
		if _, err := os.Stat(playlistPath); err == nil {
			// Video already exists, skip
			continue
		}

		// Check if video generation is already in progress for this notification
		a.VideoGenMutex.Lock()
		if a.VideoGenInProgress[notif.ID] {
			// Already generating, skip
			a.VideoGenMutex.Unlock()
			continue
		}
		// Mark as in progress
		a.VideoGenInProgress[notif.ID] = true
		a.VideoGenMutex.Unlock()

		// Generate video in a closure to properly handle defer cleanup
		func(n Notification) {
			// Ensure we clear the in-progress flag when done
			defer func() {
				a.VideoGenMutex.Lock()
				delete(a.VideoGenInProgress, n.ID)
				a.VideoGenMutex.Unlock()
			}()

			// Calculate duration
			duration := int(n.EndTime.Sub(n.StartTime).Seconds())
			if duration < 1 {
				duration = 10
			}

			log.Printf("Pre-generating video for notification %s (duration: %d seconds)", n.ID, duration)

			// Generate image first with times
			imagePath, err := generateNotificationImageSimple(n.Message, n.ID, n.StartTime, n.EndTime)
			if err != nil {
				log.Printf("Failed to pre-generate image for notification %s: %v", n.ID, err)
				return
			}

			// Convert end time to EST for TTS
			estLocation, err := time.LoadLocation("America/New_York")
			if err != nil {
				log.Printf("Warning: Could not load EST timezone for TTS, using UTC: %v", err)
				estLocation = time.UTC
			}
			endTimeEST := n.EndTime.In(estLocation)

			// Generate TTS audio: "Michel is in the meeting until [end_time]"
			ttsText := fmt.Sprintf("Hi Dan, this message is to tell you that Michel is in a meeting until %s and he had this message for you: %s", endTimeEST.Format("3:04 PM"), n.Message)
			audioPath, err := generateTTSAudio(ttsText, n.ID, n.RepeatCount)
			if err != nil {
				log.Printf("Failed to generate TTS audio for notification %s: %v (continuing without audio)", n.ID, err)
				audioPath = "" // Continue without audio if TTS fails
			}

			// Generate video with audio
			_, err = generateNotificationVideo(imagePath, n.ID, duration, audioPath)
			if err != nil {
				log.Printf("Failed to pre-generate video for notification %s: %v", n.ID, err)
				return
			}

			log.Printf("Pre-generated video for notification %s starting at %v", n.ID, n.StartTime)
		}(notif)
	}
}

