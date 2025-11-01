# Notification Cast Manager

A web application for scheduling and casting notifications to Chromecast devices on your local network.

## Architecture

The application consists of two Docker containers:

1. **Frontend Container** - Serves the web UI (Nginx)
2. **Backend Container** - Go service handling:
   - REST API endpoints
   - SQLite database
   - Chromecast device discovery
   - Scheduled casting management

## Features

- Web interface for scheduling notifications
- Device discovery and selection
- Time-based scheduling (start/end times)
- Automatic casting when start time is reached
- Automatic stop when end time is reached
- View and manage scheduled notifications
- High-quality Text-to-Speech (Google Cloud TTS) with customizable repeat count
- Generated video content with notification details (start/end times, message)
- Pre-generation of videos to minimize casting delays

## Prerequisites

- Docker and Docker Compose
- Traefik running with `traefik_proxy` network (optional, for HTTPS access)
- Authentik configured for authentication (optional)
- Chromecast devices on the same network (192.168.1.x)
- Google Cloud account with Text-to-Speech API enabled

## Installation

### 1. Clone the Repository

```bash
git clone <repository-url>
cd meetingCaster
```

### 2. Set Up Google Cloud Text-to-Speech API

The application uses Google Cloud Text-to-Speech for audio generation. Follow these steps to get your API key:

#### a. Create a Google Cloud Project

1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Create a new project or select an existing one
3. Note your Project ID

#### b. Enable Text-to-Speech API

1. In the Google Cloud Console, go to **APIs & Services** > **Library**
2. Search for "Cloud Text-to-Speech API"
3. Click on it and press **Enable**

#### c. Create a Service Account

1. Go to **APIs & Services** > **Credentials**
2. Click **Create Credentials** > **Service Account**
3. Enter a name (e.g., "notification-tts")
4. Click **Create and Continue**
5. Grant the role: **Cloud Text-to-Speech User** (or **Owner** for broader access)
6. Click **Done**

#### d. Generate and Download the Key

1. Click on the service account you just created
2. Go to the **Keys** tab
3. Click **Add Key** > **Create New Key**
4. Choose **JSON** format
5. Click **Create**
6. The key file will download automatically (e.g., `your-project-id-abc123.json`)

#### e. Place the Key in Your Project

1. Rename the downloaded file to `tts-key.json`
2. Place it in the `backend/` directory:
   ```bash
   mv ~/Downloads/your-project-id-abc123.json backend/tts-key.json
   ```
3. **Important:** This file contains sensitive credentials. Never commit it to version control.

### 3. Configure the Application

Edit `docker-compose.yml` if needed to update:
- Backend URL (must be accessible to Chromecast devices)
- Port mappings
- Domain names (if using Traefik)

### 4. Create Required Networks

If using Traefik:
```bash
docker network create traefik_proxy
```

### 5. Build and Start the Containers

```bash
docker compose up -d --build
```

This will:
- Build both frontend and backend containers
- Create necessary volumes for persistent data
- Start the services

### 6. Verify Installation

Check that containers are running:
```bash
docker compose ps
```

Check backend logs for any errors:
```bash
docker compose logs -f notification-backend
```

You should see:
- Device discovery logs (mDNS)
- Scheduler running every 10 seconds
- No TTS or database errors

### 7. Access the Web Interface

- **With Traefik:** `https://notification.milkam.ca` (or your configured domain)
- **Without Traefik:** `http://localhost:8080` (after exposing port 80 in docker-compose.yml)

## Configuration

### Environment Variables

**Backend:**
- `PORT` - Backend server port (default: 8080)
- `DB_PATH` - Database file path (default: /data/notifications.db)
- `BACKEND_URL` - URL accessible to Chromecast devices (default: http://192.168.1.3:8081)
- `GOOGLE_APPLICATION_CREDENTIALS` - Path to TTS service account key (set in Dockerfile)

**Frontend:**
- Automatically proxies API requests to backend

### Network Requirements

- The backend container exposes port 8081 on the host (mapped from container port 8080) to allow Chromecast devices to access notification content
- Chromecast devices must be able to reach the backend URL (192.168.1.3:8081)
- Ensure your firewall allows connections from Chromecast devices to port 8081
- The backend uses mDNS for device discovery, which requires multicast support

### Text-to-Speech Configuration

The application is configured to use:
- **Voice:** `en-US-Chirp-HD-F` (Google Cloud Neural2 voice)
- **Format:** MP3 at 16kHz mono (optimized for fast generation)
- **Message:** "Hi Dan, this message is to tell you that Michel is in a meeting until [END_TIME] and he had this message for you: [MESSAGE]"
- Times are automatically converted from UTC to EST for display and speech

## Usage

### Scheduling a Notification

1. **Open the web interface** at `https://notification.milkam.ca`
2. **Select a device** from the dropdown (click "Refresh Devices" if needed)
3. **Enter your message** in the text box
4. **Set start and end times** (in your local timezone)
5. **Set repeat count** (1-100) - how many times the TTS message should repeat
6. **Click "Schedule Notification"**

The system will automatically:
- Pre-generate the video with TTS audio 3-5 minutes before start time
- Start casting when the start time is reached
- Play the video with repeated audio messages
- Stop casting when the end time is reached
- Update the notification status in real-time

### Managing Notifications

- View all scheduled, active, and completed notifications in the main interface
- Delete notifications that are no longer needed
- Status indicators show:
  - **Pending** - Scheduled but not yet started
  - **Active** - Currently casting
  - **Completed** - Finished casting

### Video Generation

Videos are automatically generated with:
- **Resolution:** 1280x800
- **Content:** Gradient background with notification message, start time, and end time
- **Duration:** Matches the notification duration (start to end time)
- **Audio:** Google Cloud TTS repeated as specified, with silent padding to match video length
- **Format:** HLS (HTTP Live Streaming) for optimal Chromecast compatibility

## API Endpoints

- `GET /api/devices` - Get list of available Chromecast devices
- `POST /api/notifications` - Create a new notification (with message, device, start_time, end_time, repeat_count)
- `GET /api/notifications` - Get all notifications
- `GET /api/notifications/:id` - Get a specific notification
- `DELETE /api/notifications/:id` - Delete a notification
- `GET /notification-image/:id` - Serve generated PNG image for notification
- `GET /notification-video/:id/playlist.m3u8` - Serve HLS video playlist
- `GET /notification-video/:id/*.ts` - Serve HLS video segments

## Database Schema

The `notifications` table has the following columns:
- `id` - Unique identifier (UUID)
- `message` - The message to display
- `start_time` - When to start casting (stored in UTC)
- `end_time` - When to stop casting (stored in UTC)
- `device` - Device name/identifier
- `status` - Current status (pending, active, completed)
- `repeat_count` - How many times to repeat the TTS message (default: 1)
- `created_at` - Creation timestamp

## Troubleshooting

### Devices not showing up
- Ensure Chromecast devices are on the same network as the Docker host
- Check that mDNS/Bonjour is working in the Docker container
- Try refreshing devices manually using the "Refresh Devices" button
- Check logs: `docker compose logs notification-backend | grep mdns`

### Casting not working
- Verify the backend URL is accessible from Chromecast devices
- Test URL accessibility: `curl http://192.168.1.3:8081/api/devices` (from another machine)
- Check backend logs: `docker compose logs -f notification-backend`
- Ensure the device is selected correctly
- Verify the video playlist was generated: Check for `playlist.m3u8` in container logs

### Text-to-Speech errors
- **Error: "could not find default credentials"**
  - Ensure `tts-key.json` exists in the `backend/` directory
  - Verify the file is valid JSON
  - Rebuild containers: `docker compose up -d --build`
  
- **Error: "API has not been used in project"**
  - Go to Google Cloud Console and enable the Text-to-Speech API
  - Wait a few minutes for the API to activate
  
- **Error: "Permission denied" or "insufficient authentication scopes"**
  - Ensure the service account has the "Cloud Text-to-Speech User" role
  - Re-create and download a new key if needed

### Video generation issues
- **Videos not appearing or taking too long**
  - Check available disk space: `df -h`
  - Clean Docker build cache: `docker builder prune -af`
  - Monitor logs during video generation
  - Videos are pre-generated 3-5 minutes before start time

- **Chromecast shows casting icon but no video**
  - Verify the HLS playlist is accessible: `wget http://192.168.1.3:8081/notification-video/{id}/playlist.m3u8`
  - Check that ffmpeg completed successfully in logs
  - Ensure firewall allows connections from Chromecast to port 8081

### Notifications stuck in "pending" status
- Check scheduler logs: `docker compose logs notification-backend | grep SCHEDULER`
- Verify system time is correct: `date`
- Ensure video pre-generation completed successfully
- Check if notification times are in the past

### Port conflicts
- Change the backend port in docker-compose.yml if 8081 is already in use
- Update the BACKEND_URL environment variable accordingly
- Restart containers after changes: `docker compose restart`

### Database errors
- **Error: "no such column: repeat_count"**
  - Delete the old database: `docker compose down -v`
  - Restart: `docker compose up -d`
  - Or manually add column: `ALTER TABLE notifications ADD COLUMN repeat_count INTEGER DEFAULT 1;`

### High CPU usage
- Video generation can be CPU-intensive
- Multiple concurrent pre-generations may cause spikes
- Optimized settings already use `ultrafast` preset and reduced quality
- Consider staggering notification times to avoid simultaneous generation

## Development

### Making Changes

To rebuild after making code changes:

```bash
docker compose down
docker compose up -d --build
```

### Viewing Logs

To view real-time logs:

```bash
# Backend logs (includes scheduler, TTS, video generation)
docker compose logs -f notification-backend

# Frontend logs
docker compose logs -f notification-frontend

# All logs
docker compose logs -f
```

### Testing Locally

To test without Traefik:
1. Modify `docker-compose.yml` to expose frontend port:
   ```yaml
   frontend:
     ports:
       - "8080:80"
   ```
2. Access at `http://localhost:8080`

### Project Structure

```
meetingCaster/
├── backend/
│   ├── main.go           # API endpoints, server setup
│   ├── scheduler.go      # Notification scheduling logic
│   ├── casting.go        # Chromecast device discovery and casting
│   ├── image.go          # Image and video generation, TTS
│   ├── go.mod            # Go dependencies
│   ├── Dockerfile        # Backend container build
│   └── tts-key.json      # Google Cloud TTS credentials (not in git)
├── frontend/
│   ├── index.html        # Web UI structure
│   ├── app.js            # Frontend JavaScript logic
│   ├── styles.css        # UI styling
│   ├── nginx.conf        # Nginx configuration
│   └── Dockerfile        # Frontend container build
└── docker-compose.yml    # Multi-container orchestration
```

## Maintenance

### Cleaning Up Generated Files

Generated videos and images are stored in Docker volumes. To clean up:

```bash
# View current disk usage
docker compose exec notification-backend du -sh /data/*

# Remove completed notifications (this won't delete their generated files)
# Generated files are automatically cleaned up when videos are regenerated
```

### Cleaning Docker Build Cache

If disk space is running low:

```bash
# Clean build cache (safe, will be regenerated as needed)
docker builder prune -af

# Clean unused images (be careful with this)
docker image prune -af

# Full cleanup (removes everything not currently in use)
docker system prune -af --volumes
```

### Backing Up Data

To backup your notifications database:

```bash
# Find the volume name
docker volume ls | grep notification

# Copy database out
docker compose cp notification-backend:/data/notifications.db ./backup.db
```

### Updating the Application

```bash
# Pull latest changes
git pull

# Rebuild and restart
docker compose down
docker compose up -d --build
```

## Security Considerations

- The `tts-key.json` file contains sensitive credentials
  - Never commit it to version control
  - Ensure `.gitignore` includes `tts-key.json`
  - Restrict file permissions: `chmod 600 backend/tts-key.json`
  
- The backend is exposed on port 8081 for Chromecast access
  - Consider firewall rules to restrict access to local network only
  - Use Traefik with authentication for the web interface

- Database contains notification messages
  - Stored in Docker volume
  - Consider encrypting sensitive messages before scheduling

## Performance Optimization

Current optimizations:
- Video pre-generation 3-5 minutes before notification start
- Optimized ffmpeg settings (`ultrafast` preset, lower bitrates)
- 16kHz mono audio for TTS
- Goroutine-based pre-generation to avoid blocking
- Mutex-protected concurrent generation prevention

## Known Limitations

- Only supports one notification per device at a time
- Video duration capped by available disk space
- TTS is in English only (configurable in `image.go`)
- Times displayed in EST (hardcoded timezone)
- Requires Google Cloud account for TTS

## License

This project uses:
- [go-chromecast library](https://github.com/milkam/gochromecast) - Chromecast communication a fork of https://github.com/vjerci/gochromecast
- [Google Cloud Text-to-Speech API](https://cloud.google.com/text-to-speech) - Audio generation
- [FFmpeg](https://ffmpeg.org/) - Video encoding

## Credits

Developed for scheduling meeting notifications on Chromecast devices with high-quality text-to-speech and visual content.

