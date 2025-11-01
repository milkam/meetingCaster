package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/milkam/gochromecast/pkg/chromecast"
	"github.com/milkam/gochromecast/pkg/mdns"
	"github.com/milkam/gochromecast/pkg/ip"
	"github.com/milkam/gochromecast/pkg/server"
)

// CastSession represents an active casting session
type CastSession struct {
	NotificationID string
	Device         string
	CastClient     *chromecast.Client
	Context        context.Context
	Cancel         context.CancelFunc
	Active         bool
	Mutex          sync.RWMutex
}

var (
	discoveredDevices []ChromecastDevice
	deviceMutex       sync.RWMutex
)

func (a *App) startDeviceDiscovery() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	// Initial discovery
	a.discoverDevices()

	for range ticker.C {
		a.discoverDevices()
	}
}

func (a *App) discoverDevices() []ChromecastDevice {
	//log.Println("Discovering Chromecast devices...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use gochromecast mDNS library for discovery
	mdnsClient := mdns.New(ctx, &mdns.Config{
		IPv6: false,
	})
	
	mdnsClient.Start()

	// Wait for devices to be discovered
	time.Sleep(5 * time.Second)

	devicesChan := mdnsClient.GetDevices()
	devices := <-devicesChan
	
	// Client will clean up when context is cancelled

	var foundDevices []ChromecastDevice
	seen := make(map[string]bool)

	for _, device := range devices {
		// Extract device name (use first name from Names array)
		deviceName := ""
		if len(device.Names) > 0 {
			deviceName = device.Names[0]
		}
		
		// Fallback to URL if no name
		if deviceName == "" {
			deviceName = device.Url
		}

		// Use URL as unique identifier
		if seen[device.Url] {
			continue
		}
		seen[device.Url] = true

		foundDevices = append(foundDevices, ChromecastDevice{
			Name:    deviceName,
			UUID:    device.Url,  // Store URL as UUID so we can find device later
			Address: device.Url,
		})
		//log.Printf("Found device: %s (%s) - Names: %v", deviceName, device.Url, device.Names)
	}

	deviceMutex.Lock()
	discoveredDevices = foundDevices
	deviceMutex.Unlock()

	if len(foundDevices) == 0 {
		return getCachedDevices()
	}

	return foundDevices
}

func getCachedDevices() []ChromecastDevice {
	deviceMutex.RLock()
	defer deviceMutex.RUnlock()
	return discoveredDevices
}

func (a *App) startCast(notifID, deviceName, message string) error {
	a.CastMutex.Lock()
	defer a.CastMutex.Unlock()

	// Check if already casting
	if _, exists := a.ActiveCasts[notifID]; exists {
		return fmt.Errorf("cast already active for this notification")
	}

	// Use hardcoded values instead of flags (flags can't be redefined)
	waitTime := 5     // 5 seconds for mDNS search
	ipv6 := false     // use IPv4
	targetDeviceName := deviceName
	
	deviceToUse, err := getDevice(&ipv6, &waitTime, &targetDeviceName)
	if err != nil {
		return fmt.Errorf("failed to find device: %w", err)
	}

	// Get local IP address (needed for server.Start URL)
	localIP, err := ip.GetLANIp()
	if err != nil {
		return fmt.Errorf("failed to get local IP: %w", err)
	}
	log.Printf("Resolved local IP to %s", localIP)

	castCtx, castCancel := context.WithCancel(context.Background())

	// Create Chromecast client using gochromecast library
	client := chromecast.New(castCtx, &chromecast.Config{
		Device: deviceToUse,
	})

	// Start the HLS server (from gochromecast/pkg/server)
	// This serves files from ./data/chunks/ on port 8889
	const serverPort = ":8889"
	go server.Start(serverPort)

	// Wait for server to start
	time.Sleep(1 * time.Second)

	// Create URL using the local IP and server port
	// This matches the working example: http://IP:PORT/files/notificationID/playlist.m3u8
	notificationURL := fmt.Sprintf("http://%s%s/files/%s/playlist.m3u8", localIP, serverPort, notifID)
	log.Printf("Casting URL: %s to device: %s", notificationURL, deviceToUse.Url)

	// Play media using the chromecast library
	err = client.PlayMedia(castCtx, chromecast.PlayMediaRequest{
		ChromeCastDeviceURI: deviceToUse.Url,
		MediaURL:            notificationURL,
	})
	if err != nil {
		castCancel()
		return fmt.Errorf("failed to cast media: %w", err)
	}

	log.Printf("Successfully casting notification %s to device %s", notifID, deviceName)

	session := &CastSession{
		NotificationID: notifID,
		Device:         deviceName,
		CastClient:     client,
		Context:        castCtx,
		Cancel:         castCancel,
		Active:         true,
	}

	a.ActiveCasts[notifID] = session

	// Update database status
	_, err = a.DB.Exec("UPDATE notifications SET status = 'active' WHERE id = ?", notifID)
	if err != nil {
		log.Printf("Failed to update notification status: %v", err)
	}

	log.Printf("Started casting notification %s to device %s", notifID, deviceName)
	return nil
}

func (a *App) stopCast(notifID string) error {
	log.Printf("Stopping cast for notification %s", notifID)
	a.CastMutex.Lock()
	defer a.CastMutex.Unlock()

	session, exists := a.ActiveCasts[notifID]
	if !exists {
		return nil // Already stopped or never started
	}

	session.Mutex.Lock()
	if !session.Active {
		session.Mutex.Unlock()
		return nil
	}
	session.Active = false // Mark as inactive
	session.Mutex.Unlock()

	// Cancel context to close the connection - Chromecast will handle cleanup
	if session.Cancel != nil {
		log.Printf("Stopping in session.cancel cast for notification %s", notifID)
		session.Cancel()
		log.Printf("Cast stopped in session.cancel for notification %s", notifID)
	}
	
	// Give Chromecast a moment to process the disconnection
	time.Sleep(1500 * time.Millisecond)

	delete(a.ActiveCasts, notifID)

	// Update database status
	_, err := a.DB.Exec("UPDATE notifications SET status = 'completed' WHERE id = ?", notifID)
	if err != nil {
		log.Printf("Failed to update notification status: %v", err)
	}

	log.Printf("Stopped casting notification %s", notifID)
	return nil
}

func getDevice(ipv6 *bool, waitTime *int, targetDevice *string) (mdns.Device, error) {
	mdnsCtx, mdnsCancel := context.WithCancel(context.Background())
	mdnsClient := mdns.New(mdnsCtx, &mdns.Config{
		IPv6: *ipv6,
	})

	mdnsClient.Start()

	time.Sleep(time.Duration(*waitTime) * time.Second)

	devicesChan := mdnsClient.GetDevices()

	devices := <-devicesChan

	mdnsCancel()

	for _, device := range devices {
		for _, name := range device.Names {
			if name == *targetDevice {
				return device, nil
			}
		}
	}

	return mdns.Device{}, fmt.Errorf("failed to find device for name '%s'", *targetDevice)
}
