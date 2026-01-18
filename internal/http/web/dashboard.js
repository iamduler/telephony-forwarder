// Dashboard JavaScript Logic
let autoRefreshInterval = null;
let currentTab = 'success';
let readFromLogs = false;

function formatTime(timestamp) {
    const date = new Date(timestamp);
    return date.toLocaleString('vi-VN', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit'
    });
}

function formatRelativeTime(timestamp) {
    const date = new Date(timestamp);
    const now = new Date();
    const diff = Math.floor((now - date) / 1000);
    
    if (diff < 60) return `${diff} giây trước`;
    if (diff < 3600) return `${Math.floor(diff / 60)} phút trước`;
    if (diff < 86400) return `${Math.floor(diff / 3600)} giờ trước`;
    return `${Math.floor(diff / 86400)} ngày trước`;
}

function getStateBadge(state) {
    const badges = {
        'answered': '<span class="badge badge-success">✓ Answered</span>',
        'missed': '<span class="badge badge-warning">✗ Missed</span>',
        'busy': '<span class="badge badge-warning">📞 Busy</span>',
        'completed': '<span class="badge badge-success">✓ Completed</span>',
    };
    return badges[state] || `<span class="badge badge-info">${state}</span>`;
}

function renderEvents(eventsByDomain, failedEventsByDomain) {
    const container = document.getElementById('domainsContainer');
    const domainFilter = document.getElementById('domainFilter').value.toLowerCase();

    // Filter based on current tab
    let eventsToShow = {};
    let failedToShow = {};

    if (currentTab === 'success' || currentTab === 'all') {
        eventsToShow = eventsByDomain || {};
    }
    if (currentTab === 'failed' || currentTab === 'all') {
        failedToShow = failedEventsByDomain || {};
    }

    const hasEvents = Object.keys(eventsToShow).length > 0 || Object.keys(failedToShow).length > 0;

    if (!hasEvents) {
        container.innerHTML = `
            <div class="empty-state">
                <div class="empty-state-icon">📭</div>
                <h2>Chưa có events nào</h2>
                <p>Gửi events để xem chúng ở đây</p>
            </div>
        `;
        return;
    }

    let html = '';
    
    // Get all unique domains
    const allDomains = new Set();
    Object.keys(eventsToShow).forEach(d => allDomains.add(d));
    Object.keys(failedToShow).forEach(d => allDomains.add(d));
    const domains = Array.from(allDomains).sort();

    domains.forEach(domain => {
        if (domainFilter && !domain.toLowerCase().includes(domainFilter)) {
            return;
        }

        const events = eventsToShow[domain] || [];
        const failedEvents = failedToShow[domain] || [];
        const totalCount = events.length + failedEvents.length;

        if (totalCount === 0) return;
        html += `
            <div class="domain-card">
                <div class="domain-header">
                    <div class="domain-name">🌐 ${domain}</div>
                    <div class="domain-count">
                        ${events.length > 0 ? `<span style="color: #28a745;">✓${events.length}</span>` : ''}
                        ${failedEvents.length > 0 ? `<span style="color: #dc3545; margin-left: 8px;">✗${failedEvents.length}</span>` : ''}
                    </div>
                </div>
                <div class="events-list">
                    ${events.map(event => {
                        let eventData = {};
                        try {
                            eventData = typeof event.event === 'string' 
                                ? JSON.parse(event.event) 
                                : event.event;
                        } catch (e) {
                            console.error('Error parsing event data:', e, event.event);
                            eventData = {};
                        }
                        return `
                            <div class="event-card">
                                <div class="event-header">
                                    <div class="event-call-id">📞 ${event.call_id || 'N/A'}</div>
                                    <div class="event-time" title="${formatTime(event.forwarded_at)}">
                                        ${formatRelativeTime(event.forwarded_at)}
                                    </div>
                                </div>
                                <div class="event-details">
                                    <div class="event-detail">
                                        <div class="event-detail-label">Direction</div>
                                        <div class="event-detail-value">${eventData.direction || 'N/A'}</div>
                                    </div>
                                    <div class="event-detail">
                                        <div class="event-detail-label">State</div>
                                        <div class="event-detail-value">${getStateBadge(eventData.state || 'unknown')}</div>
                                    </div>
                                    <div class="event-detail">
                                        <div class="event-detail-label">Status</div>
                                        <div class="event-detail-value">${eventData.status || 'N/A'}</div>
                                    </div>
                                    <div class="event-detail">
                                        <div class="event-detail-label">From</div>
                                        <div class="event-detail-value">${eventData.from_number || 'N/A'}</div>
                                    </div>
                                    <div class="event-detail">
                                        <div class="event-detail-label">To</div>
                                        <div class="event-detail-value">${eventData.to_number || eventData.hotline || 'N/A'}</div>
                                    </div>
                                    <div class="event-detail">
                                        <div class="event-detail-label">Attempt</div>
                                        <div class="event-detail-value">#${event.delivery_attempt}</div>
                                    </div>
                                </div>
                                ${event.endpoints && event.endpoints.length > 0 ? `
                                    <div class="endpoints-list">
                                        <strong style="font-size: 12px; color: #6c757d;">Endpoints:</strong>
                                        ${event.endpoints.map(ep => `<span class="endpoint">${ep}</span>`).join('')}
                                    </div>
                                ` : ''}
                            </div>
                        `;
                    }).join('')}
                    ${failedEvents.map(event => {
                        // Handle both in-memory store format and log file format
                        let eventData = {};
                        if (event.event) {
                            // From in-memory store: parse event.event
                            try {
                                eventData = typeof event.event === 'string' 
                                    ? JSON.parse(event.event) 
                                    : event.event;
                            } catch (e) {
                                console.error('Error parsing failed event data:', e, event.event);
                                eventData = {};
                            }
                        } else {
                            // From log files: use direct fields
                            eventData = {
                                direction: event.direction,
                                state: event.state,
                                status: event.status,
                                from_number: event.from_number,
                                to_number: event.to_number,
                                hotline: event.hotline
                            };
                        }
                        
                        // Determine if will retry (from in-memory store) or based on delivery_attempt
                        const willRetry = event.will_retry !== undefined 
                            ? event.will_retry 
                            : (event.delivery_attempt && event.max_deliveries && event.delivery_attempt < event.max_deliveries);
                        
                        const cardClass = willRetry ? 'retry' : 'failed';
                        const attemptDisplay = event.max_deliveries 
                            ? `#${event.delivery_attempt}/${event.max_deliveries}`
                            : `#${event.delivery_attempt}`;
                        
                        return `
                            <div class="event-card ${cardClass}">
                                <div class="event-header">
                                    <div class="event-call-id">
                                        ${willRetry ? '🔄' : '❌'} ${event.call_id || 'N/A'}
                                        ${willRetry ? '<span class="badge badge-warning" style="margin-left: 8px;">Will Retry</span>' : ''}
                                    </div>
                                    <div class="event-time" title="${formatTime(event.failed_at)}">
                                        ${formatRelativeTime(event.failed_at)}
                                    </div>
                                </div>
                                <div class="event-details">
                                    <div class="event-detail">
                                        <div class="event-detail-label">Direction</div>
                                        <div class="event-detail-value">${eventData.direction || event.direction || 'N/A'}</div>
                                    </div>
                                    <div class="event-detail">
                                        <div class="event-detail-label">State</div>
                                        <div class="event-detail-value">${getStateBadge(eventData.state || event.state || 'unknown')}</div>
                                    </div>
                                    <div class="event-detail">
                                        <div class="event-detail-label">Status</div>
                                        <div class="event-detail-value">${eventData.status || event.status || 'N/A'}</div>
                                    </div>
                                    <div class="event-detail">
                                        <div class="event-detail-label">Attempt</div>
                                        <div class="event-detail-value">${attemptDisplay}</div>
                                    </div>
                                </div>
                                ${event.error || (event.error_messages && event.error_messages.length > 0) ? `
                                    <div class="error-messages">
                                        <strong style="font-size: 12px; color: #721c24;">Errors:</strong>
                                        ${event.error ? `<div class="error-message">${event.error}</div>` : ''}
                                        ${event.error_messages ? event.error_messages.map(err => `<div class="error-message">${err}</div>`).join('') : ''}
                                    </div>
                                ` : ''}
                                ${event.endpoints && event.endpoints.length > 0 ? `
                                    <div class="endpoints-list">
                                        <strong style="font-size: 12px; color: #6c757d;">Endpoints:</strong>
                                        ${event.endpoints.map(ep => `<span class="endpoint">${ep}</span>`).join('')}
                                    </div>
                                ` : ''}
                            </div>
                        `;
                    }).join('')}
                </div>
            </div>
        `;
    });

    container.innerHTML = html || `
        <div class="empty-state">
            <div class="empty-state-icon">🔍</div>
            <h2>Không tìm thấy domain nào</h2>
            <p>Thử tìm kiếm với từ khóa khác</p>
        </div>
    `;
}

async function loadEvents() {
    const loading = document.getElementById('loading');
    const domainFilter = document.getElementById('domainFilter').value;
    const dateFilter = document.getElementById('dateFilter').value;

    loading.style.display = 'block';

    try {
        let url, params;
        
        if (readFromLogs) {
            // Read from log files
            url = '/api/logs';
            params = new URLSearchParams();
            if (domainFilter) {
                params.append('domain', domainFilter);
            }
            if (dateFilter) {
                params.append('date', dateFilter);
            }
        } else {
            // Read from in-memory store
            url = '/api/events';
            params = new URLSearchParams();
            if (domainFilter) {
                params.append('domain', domainFilter);
            }
            if (currentTab !== 'all') {
                params.append('type', currentTab);
            }
        }
        
        if (params.toString()) {
            url += '?' + params.toString();
        }
        
        const response = await fetch(url);
        
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }
        
        const data = await response.json();
        
        // Debug: log data structure
        if (data && typeof data === 'object') {
            console.log('API Response:', {
                hasEvents: !!data.events_by_domain,
                hasFailedEvents: !!data.failed_events_by_domain,
                stats: data.stats,
                source: readFromLogs ? 'log files' : 'in-memory store'
            });
        }

        // Update stats
        if (data.stats) {
            document.getElementById('totalSuccessful').textContent = data.stats.total_successful || 0;
            document.getElementById('totalFailed').textContent = data.stats.total_failed || 0;
            document.getElementById('retryCount').textContent = data.stats.retry_count || 0;
            document.getElementById('totalDomains').textContent = data.stats.domains || 0;
        }

        // Render events
        renderEvents(data.events_by_domain || {}, data.failed_events_by_domain || {});

    } catch (error) {
        console.error('Error loading events:', error);
        let errorMessage = error.message || 'Unknown error';
        
        // Better error message for JSON parse errors
        if (errorMessage.includes('JSON') || errorMessage.includes('parse')) {
            errorMessage = 'Lỗi khi parse dữ liệu từ server. Vui lòng refresh lại trang.';
        }
        
        document.getElementById('domainsContainer').innerHTML = `
            <div class="empty-state">
                <div class="empty-state-icon">❌</div>
                <h2>Lỗi khi tải dữ liệu</h2>
                <p>${errorMessage}</p>
                <p style="margin-top: 12px; font-size: 12px; color: #6c757d;">
                    Kiểm tra console để xem chi tiết lỗi (F12)
                </p>
            </div>
        `;
    } finally {
        loading.style.display = 'none';
    }
}

function toggleAutoRefresh() {
    const checkbox = document.getElementById('autoRefresh');
    
    if (checkbox.checked) {
        autoRefreshInterval = setInterval(loadEvents, 5000); // Refresh every 5 seconds
    } else {
        if (autoRefreshInterval) {
            clearInterval(autoRefreshInterval);
            autoRefreshInterval = null;
        }
    }
}

function switchTab(tab) {
    currentTab = tab;
    
    // Update tab buttons
    document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    document.getElementById('tab' + tab.charAt(0).toUpperCase() + tab.slice(1)).classList.add('active');
    
    // Reload events
    loadEvents();
}

function toggleReadFromLogs() {
    readFromLogs = document.getElementById('readFromLogs').checked;
    const dateFilter = document.getElementById('dateFilter');
    
    // Show/hide date filter based on readFromLogs
    dateFilter.style.display = readFromLogs ? 'block' : 'none';
    
    // Set default date to today if reading from logs
    if (readFromLogs && !dateFilter.value) {
        const today = new Date().toISOString().split('T')[0];
        dateFilter.value = today;
    }
    
    // Reload events
    loadEvents();
}

// Initialize on page load
document.addEventListener('DOMContentLoaded', function() {
    // Filter input handler
    document.getElementById('domainFilter').addEventListener('input', function() {
        loadEvents();
    });

    // Initial load
    loadEvents();
});
