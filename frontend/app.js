const API_BASE_URL = '/api';

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => {
    loadDevices();
    loadNotifications();
    
    document.getElementById('refreshDevices').addEventListener('click', loadDevices);
    document.getElementById('notificationForm').addEventListener('submit', handleSubmit);
    
    // Refresh notifications every 5 seconds
    setInterval(loadNotifications, 5000);
});

async function loadDevices() {
    const deviceSelect = document.getElementById('device');
    deviceSelect.innerHTML = '<option value="">Loading devices...</option>';
    
    try {
        const response = await fetch(`${API_BASE_URL}/devices`);
        if (!response.ok) throw new Error('Failed to load devices');
        
        const devices = await response.json();
        
        if (devices.length === 0) {
            deviceSelect.innerHTML = '<option value="">No devices found</option>';
            return;
        }
        
        deviceSelect.innerHTML = '<option value="">Select a device...</option>';
        devices.forEach(device => {
            const option = document.createElement('option');
            option.value = device.name;  // Use device name instead of UUID
            option.textContent = `${device.name} (${device.address})`;
            deviceSelect.appendChild(option);
        });
    } catch (error) {
        console.error('Error loading devices:', error);
        deviceSelect.innerHTML = '<option value="">Error loading devices</option>';
    }
}

async function loadNotifications() {
    const list = document.getElementById('notificationsList');
    
    try {
        const response = await fetch(`${API_BASE_URL}/notifications`);
        if (!response.ok) throw new Error('Failed to load notifications');
        
        const notifications = await response.json();
        
        if (notifications.length === 0) {
            list.innerHTML = '<p class="empty">No scheduled notifications</p>';
            return;
        }
        
        list.innerHTML = notifications.map(notif => createNotificationCard(notif)).join('');
        
        // Add delete button handlers
        notifications.forEach(notif => {
            const deleteBtn = document.querySelector(`[data-id="${notif.id}"]`);
            if (deleteBtn) {
                deleteBtn.addEventListener('click', () => deleteNotification(notif.id));
            }
        });
    } catch (error) {
        console.error('Error loading notifications:', error);
        list.innerHTML = '<p class="error">Error loading notifications</p>';
    }
}

function createNotificationCard(notif) {
    const startTime = new Date(notif.start_time).toLocaleString();
    const endTime = new Date(notif.end_time).toLocaleString();
    const statusClass = notif.status;
    
    return `
        <div class="notification-card ${statusClass}">
            <div class="notification-header">
                <span class="status-badge status-${notif.status}">${notif.status}</span>
                <button class="btn-danger" data-id="${notif.id}">Delete</button>
            </div>
            <div class="notification-message">${escapeHtml(notif.message)}</div>
            <div class="notification-info">
                <div><strong>Device:</strong> ${escapeHtml(notif.device)}</div>
                <div><strong>Start:</strong> ${startTime}</div>
                <div><strong>End:</strong> ${endTime}</div>
            </div>
        </div>
    `;
}

async function handleSubmit(e) {
    e.preventDefault();
    
    const formData = {
        message: document.getElementById('message').value,
        device: document.getElementById('device').value,
        start_time: new Date(document.getElementById('startTime').value).toISOString(),
        end_time: new Date(document.getElementById('endTime').value).toISOString(),
        repeat_count: parseInt(document.getElementById('repeatCount').value) || 1,
    };
    
    if (!formData.device) {
        alert('Please select a device');
        return;
    }
    
    if (formData.start_time >= formData.end_time) {
        alert('End time must be after start time');
        return;
    }
    
    try {
        const response = await fetch(`${API_BASE_URL}/notifications`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify(formData),
        });
        
        if (!response.ok) {
            const error = await response.json();
            throw new Error(error.error || 'Failed to create notification');
        }
        
        // Reset form
        document.getElementById('notificationForm').reset();
        loadNotifications();
        alert('Notification scheduled successfully!');
    } catch (error) {
        console.error('Error creating notification:', error);
        alert('Error: ' + error.message);
    }
}

async function deleteNotification(id) {
    if (!confirm('Are you sure you want to delete this notification?')) {
        return;
    }
    
    try {
        const response = await fetch(`${API_BASE_URL}/notifications/${id}`, {
            method: 'DELETE',
        });
        
        if (!response.ok) throw new Error('Failed to delete notification');
        
        loadNotifications();
    } catch (error) {
        console.error('Error deleting notification:', error);
        alert('Error deleting notification');
    }
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

