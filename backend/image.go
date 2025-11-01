package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/fogleman/gg"
)


// wrapText wraps text into multiple lines
func wrapText(text string, maxWidth int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}

	var lines []string
	currentLine := words[0]

	for _, word := range words[1:] {
		testLine := currentLine + " " + word
		if len(testLine) <= maxWidth {
			currentLine = testLine
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}
	lines = append(lines, currentLine)

	return lines
}


// generateNotificationImageSimple creates a simpler PNG image with message and times
func generateNotificationImageSimple(message string, notificationID string, startTime, endTime time.Time) (string, error) {
    // Create images directory if it doesn't exist
    imagesDir := "/data/images"
    if err := os.MkdirAll(imagesDir, 0755); err != nil {
        return "", fmt.Errorf("failed to create images directory: %w", err)
    }

    // Image dimensions (New Resolution: 1280x800)
    width := 1280
    height := 800

    // Create a new image with gradient
    dc := gg.NewContext(width, height)

    // Draw gradient background
    gradient := gg.NewLinearGradient(0, 0, float64(width), float64(height))
    gradient.AddColorStop(0, color.RGBA{102, 126, 234, 255}) // #667eea
    gradient.AddColorStop(1, color.RGBA{118, 75, 162, 255})  // #764ba2
    dc.SetFillStyle(gradient)
    dc.DrawRectangle(0, 0, float64(width), float64(height))
    dc.Fill()

    // Load a font for the Title
    if err := dc.LoadFontFace("/usr/share/fonts/dejavu/DejaVuSans-Bold.ttf", 80); err != nil {
        log.Printf("Warning: Could not load font, text may not display correctly: %v", err)
    }
    
    dc.SetColor(color.White)

    // Convert UTC times to EST
    estLocation, err := time.LoadLocation("America/New_York")
    if err != nil {
        log.Printf("Warning: Could not load EST timezone, using UTC: %v", err)
        estLocation = time.UTC
    }
    startTimeEST := startTime.In(estLocation)
    endTimeEST := endTime.In(estLocation)

    // Format times in EST
    timeFormat := "3:04 PM MST"
    startStr := startTimeEST.Format(timeFormat)
    endStr := endTimeEST.Format(timeFormat)
    
    // Title
    title := "MEETING IN PROGRESS"
    titleWidth, _ := dc.MeasureString(title)
    // New Title Position: Moved slightly down from 200 to 180 (closer to the top)
    dc.DrawString(title, float64(width)/2-titleWidth/2, 180)

    // Message font
    if err := dc.LoadFontFace("/usr/share/fonts/dejavu/DejaVuSans-Bold.ttf", 64); err != nil {
        log.Printf("Warning: Could not load font for message: %v", err)
    }
    
    // Split message into lines for better display
    lines := wrapText(message, 30)
    maxLines := 5
    if len(lines) > maxLines {
        lines = lines[:maxLines]
    }

    // Draw message lines centered
    messageY := 350.0 
    lineSpacing := 85.0 
    
    for i, line := range lines {
        lineWidth, _ := dc.MeasureString(line)
        dc.DrawString(line, float64(width)/2-lineWidth/2, messageY+float64(i)*lineSpacing)
    }

    // Time information font
    if err := dc.LoadFontFace("/usr/share/fonts/dejavu/DejaVuSans.ttf", 48); err != nil {
        log.Printf("Warning: Could not load font for time: %v", err)
    }
    
    timeInfo := fmt.Sprintf("%s - %s", startStr, endStr)
    timeWidth, _ := dc.MeasureString(timeInfo)
    dc.DrawString(timeInfo, float64(width)/2-timeWidth/2, float64(height)-80) 

    // Save image
    imagePath := filepath.Join(imagesDir, fmt.Sprintf("%s.png", notificationID))
    if err := dc.SavePNG(imagePath); err != nil {
        return "", fmt.Errorf("failed to save image: %w", err)
    }

    return imagePath, nil
}

// generateTTSAudio creates audio from text using Google Cloud Text-to-Speech
func generateTTSAudio(text string, notificationID string, repeatCount int) (string, error) {
	audioDir := "/data/audio"
	if err := os.MkdirAll(audioDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create audio directory: %w", err)
	}

	singleAudioPath := filepath.Join(audioDir, fmt.Sprintf("%s_single.mp3", notificationID))
	
	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	// Create Google Cloud TTS client
	client, err := texttospeech.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create TTS client: %w", err)
	}
	defer client.Close()

	// Build the TTS request
	req := &texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Text{Text: text},
		},
		Voice: &texttospeechpb.VoiceSelectionParams{
			LanguageCode: "en-US",
			Name:         "en-US-Chirp-HD-F", // High quality female Chirp HD voice
			SsmlGender:   texttospeechpb.SsmlVoiceGender_FEMALE,
		},
		AudioConfig: &texttospeechpb.AudioConfig{
			AudioEncoding:   texttospeechpb.AudioEncoding_MP3,
			SpeakingRate:    1.0,   // Normal speed
			Pitch:           0.0,   // Normal pitch
			SampleRateHertz: 16000, // 16kHz - lower quality, faster generation
		},
	}

	// Perform the TTS request
	resp, err := client.SynthesizeSpeech(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to synthesize speech: %w", err)
	}

	// Write the audio content to file
	if err := os.WriteFile(singleAudioPath, resp.AudioContent, 0644); err != nil {
		return "", fmt.Errorf("failed to write audio file: %w", err)
	}

	// If repeatCount is 1, return the single audio
	if repeatCount <= 1 {
		return singleAudioPath, nil
	}

	// Create repeated audio by concatenating multiple copies
	finalAudioPath := filepath.Join(audioDir, fmt.Sprintf("%s.mp3", notificationID))
	
	// Build ffmpeg command to concatenate audio files
	var inputs []string
	for i := 0; i < repeatCount; i++ {
		inputs = append(inputs, "-i", singleAudioPath)
	}
	
	// Build filter complex for concatenation
	filterComplex := fmt.Sprintf("concat=n=%d:v=0:a=1[out]", repeatCount)
	
	args := append([]string{"-y"}, inputs...)
	args = append(args, "-filter_complex", filterComplex, "-map", "[out]", finalAudioPath)
	
	concatCmd := exec.Command("ffmpeg", args...)
	concatCmd.Stderr = os.Stderr
	if err := concatCmd.Run(); err != nil {
		// If concat fails, just use the single audio
		log.Printf("Warning: Failed to concatenate audio, using single instance: %v", err)
		return singleAudioPath, nil
	}

	return finalAudioPath, nil
}

// generateNotificationVideo creates an HLS playlist (.m3u8) from the PNG image with audio
// Chromecast works best with HLS format instead of direct MP4
func generateNotificationVideo(imagePath string, notificationID string, durationSeconds int, audioPath string) (string, error) {
	// Create chunks directory for this notification (to match server.Start expectations)
	videosDir := filepath.Join("./data/chunks", notificationID)
	if err := os.MkdirAll(videosDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create chunks directory: %w", err)
	}

	// Output HLS master playlist path (this will be the main entry point)
	masterPlaylistPath := filepath.Join(videosDir, "playlist.m3u8")
	
	// Media playlist and segment output pattern
	// The master playlist will reference this media playlist (no extension, like in example)
	segmentPattern := filepath.Join(videosDir, "%d.ts")

	// Use ffmpeg to create HLS format video from the image
	// Based on gochromecast example ffmpeg settings for Chromecast compatibility
	// Creates a master playlist that references a media playlist with segments
	var cmd *exec.Cmd
	
	if audioPath != "" {
		// With audio: use anullsrc to generate silence efficiently after audio ends
		// This prevents Chromecast from stopping when audio ends
		// anullsrc generates silence much faster than apad
		cmd = exec.Command("ffmpeg",
			"-y", // overwrite output file if it exists
			"-loop", "1", // loop the input image
			"-framerate", "1", // 1 fps (static image doesn't need high framerate)
			"-t", fmt.Sprintf("%d", durationSeconds), // duration in seconds
			"-i", imagePath, // input image
			"-i", audioPath, // input audio (already repeated as needed)
			"-f", "lavfi", // use lavfi for generating silence
			"-t", fmt.Sprintf("%d", durationSeconds), // silence duration same as video
			"-i", "anullsrc=r=16000:cl=mono", // generate silence at 16kHz mono
			"-filter_complex", "[1:a][2:a]concat=n=2:v=0:a=1[outa]", // concat TTS audio + silence
			"-map", "0:v", // map video from input 0 (image)
			"-map", "[outa]", // map concatenated audio
			"-preset", "ultrafast", // fastest encoding
			"-c:v", "libx264", // use H.264 codec
			"-c:a", "aac", // audio codec
			"-b:a", "64k", // audio bitrate
			"-ar", "16000", // audio sample rate 16kHz
			"-ac", "1", // 1 audio channel (mono)
			"-b:v", "512k", // video bitrate
			"-profile:v", "baseline", // quality settings
			"-crf", "28", // constant rate factor
			"-pix_fmt", "yuv420p", // pixel format for maximum compatibility
			"-threads", "0", // use all CPUs
			"-max_interleave_delta", "0", // fix interleaving warnings
			"-f", "hls", // output format is HLS
			"-hls_list_size", "0", // keep all segments
			"-hls_time", "10", // segment duration (10 seconds)
			"-hls_playlist_type", "event", // tell player this is an event
			"-hls_flags", "independent_segments+append_list", // allow for streaming
			"-hls_segment_filename", segmentPattern, // segment file naming pattern
			"-master_pl_name", "playlist.m3u8", // create master playlist
			filepath.Join(videosDir, "playlist"), // output media playlist (no extension)
		)
	} else {
		// Without audio: optimized for speed
		cmd = exec.Command("ffmpeg",
			"-y", // overwrite output file if it exists
			"-loop", "1", // loop the input image
			"-framerate", "1", // 1 fps (static image doesn't need high framerate)
			"-t", fmt.Sprintf("%d", durationSeconds), // duration in seconds
			"-i", imagePath, // input image
			"-preset", "ultrafast", // fastest encoding
			"-c:v", "libx264", // use H.264 codec
			"-b:v", "512k", // video bitrate (reduced from 1024k)
			"-profile:v", "baseline", // quality settings (reduced from high)
			"-crf", "28", // constant rate factor (increased from 22 = lower quality)
			"-pix_fmt", "yuv420p", // pixel format for maximum compatibility
			"-threads", "0", // use all CPUs
			"-f", "hls", // output format is HLS
			"-hls_list_size", "0", // keep all segments
			"-hls_time", "10", // segment duration (10 seconds)
			"-hls_playlist_type", "event", // tell player this is an event
			"-hls_flags", "independent_segments+append_list", // allow for streaming
			"-hls_segment_filename", segmentPattern, // segment file naming pattern
			"-master_pl_name", "playlist.m3u8", // create master playlist
			filepath.Join(videosDir, "playlist"), // output media playlist (no extension)
		)
	}

	// Capture stderr for error messages
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to create HLS video with ffmpeg: %w", err)
	}

	// Verify the master playlist file was created and has content
	if stat, err := os.Stat(masterPlaylistPath); err != nil {
		return "", fmt.Errorf("HLS master playlist was not created: %w", err)
	} else if stat.Size() == 0 {
		return "", fmt.Errorf("HLS master playlist is empty")
	}

	return masterPlaylistPath, nil
}

// decodeImageFromFile decodes an image from a file
func decodeImageFromFile(file *os.File) (image.Image, string, error) {
	img, format, err := image.Decode(file)
	if err != nil {
		return nil, "", err
	}
	return img, format, nil
}

