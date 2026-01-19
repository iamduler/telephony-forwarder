// Log Viewer JavaScript Logic (jQuery)
let autoRefreshInterval = null;

function escapeHtml(text) {
    const map = {
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#039;'
    };
    return String(text).replace(/[&<>"']/g, function(m) { return map[m]; });
}

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
        'answered': '<span class="badge badge-success"><i class="fas fa-check-circle"></i> Answered</span>',
        'missed': '<span class="badge badge-warning"><i class="fas fa-times-circle"></i> Missed</span>',
        'busy': '<span class="badge badge-warning"><i class="fas fa-phone-alt"></i> Busy</span>',
        'completed': '<span class="badge badge-success"><i class="fas fa-check-circle"></i> Completed</span>',
    };
    return badges[state] || `<span class="badge badge-info">${state}</span>`;
}

function renderEvents(eventsByDomain, failedEventsByDomain) {
    const $container = $('#domainsContainer');
    const $statsDiv = $('#stats');

    const hasEvents = Object.keys(eventsByDomain || {}).length > 0 || Object.keys(failedEventsByDomain || {}).length > 0;

    if (!hasEvents) {
        $container.html(`
            <div class="empty-state">
                <div class="empty-state-icon"><i class="fas fa-inbox"></i></div>
                <h2>Không có events nào trong log</h2>
                <p>Thử chọn domain hoặc ngày khác</p>
            </div>
        `);
        $statsDiv.hide();
        return;
    }

    $statsDiv.css('display', 'grid');

    let html = '';
    
    // Get all unique domains
    const allDomains = new Set();
    Object.keys(eventsByDomain || {}).forEach(d => allDomains.add(d));
    Object.keys(failedEventsByDomain || {}).forEach(d => allDomains.add(d));
    const domains = Array.from(allDomains).sort();

    domains.forEach(domain => {
        const events = eventsByDomain[domain] || [];
        const failedEvents = failedEventsByDomain[domain] || [];
        const totalCount = events.length + failedEvents.length;

        if (totalCount === 0) return;
        
        html += `
            <div class="domain-card">
                <div class="domain-header">
                    <div class="domain-name"><i class="fas fa-globe"></i> ${domain}</div>
                    <div class="domain-count">
                        ${events.length > 0 ? `<span style="color: #28a745;"><i class="fas fa-check-circle"></i> ${events.length}</span>` : ''}
                        ${failedEvents.length > 0 ? `<span style="color: #dc3545; margin-left: 8px;"><i class="fas fa-times-circle"></i> ${failedEvents.length}</span>` : ''}
                    </div>
                </div>
                <div class="events-list">
                    ${events.map(event => {
                        // Display raw JSON data from log file
                        const rawData = JSON.stringify(event, null, 2);
                        const escapedData = escapeHtml(rawData);
                        const timestamp = event.timestamp || event.forwarded_at || '';
                        const msg = event.msg || '';
                        
                        return `
                            <div class="event-card">
                                <div class="event-header">
                                    <div class="event-call-id">
                                        <i class="fas fa-phone"></i> ${escapeHtml(event.call_id || 'N/A')}
                                        ${msg ? `<span style="margin-left: 8px; font-size: 12px; color: #6c757d; font-weight: normal;">${escapeHtml(msg)}</span>` : ''}
                                    </div>
                                    <div class="event-time" title="${escapeHtml(timestamp)}">
                                        <i class="fas fa-clock"></i> ${escapeHtml(timestamp)}
                                    </div>
                                </div>
                                <pre style="background: #f8f9fa; padding: 12px; border-radius: 6px; overflow-x: auto; font-family: monospace; font-size: 12px; margin-top: 12px; white-space: pre-wrap; word-wrap: break-word;">${escapedData}</pre>
                            </div>
                        `;
                    }).join('')}
                    ${failedEvents.map(event => {
                        // Display raw JSON data from log file
                        const rawData = JSON.stringify(event, null, 2);
                        const escapedData = escapeHtml(rawData);
                        const willRetry = event.delivery_attempt && event.max_deliveries && event.delivery_attempt < event.max_deliveries;
                        const cardClass = willRetry ? 'retry' : 'failed';
                        const timestamp = event.timestamp || event.failed_at || '';
                        const msg = event.msg || '';
                        
                        return `
                            <div class="event-card ${cardClass}">
                                <div class="event-header">
                                    <div class="event-call-id">
                                        <i class="fas fa-phone"></i> ${escapeHtml(event.call_id || 'N/A')}
                                        ${msg ? `<span style="margin-left: 8px; font-size: 12px; color: #6c757d; font-weight: normal;">${escapeHtml(msg)}</span>` : ''}
                                        ${willRetry ? '<span class="badge badge-warning" style="margin-left: 8px;"><i class="fas fa-redo"></i> Will Retry</span>' : ''}
                                    </div>
                                    <div class="event-time" title="${escapeHtml(timestamp)}">
                                        <i class="fas fa-clock"></i> ${escapeHtml(timestamp)}
                                    </div>
                                </div>
                                <pre style="background: #f8f9fa; padding: 12px; border-radius: 6px; overflow-x: auto; font-family: monospace; font-size: 12px; margin-top: 12px; white-space: pre-wrap; word-wrap: break-word;">${escapedData}</pre>
                            </div>
                        `;
                    }).join('')}
                </div>
            </div>
        `;
    });

    $container.html(html);
}

function loadDomains() {
    $.ajax({
        url: '/api/logs/domains',
        method: 'GET',
        dataType: 'json',
        success: function(data) {
            const $domainSelect = $('#domainSelect');
            
            // Clear existing options except the first one
            $domainSelect.find('option:not(:first)').remove();
            
            // Add domains
            if (data.domains && data.domains.length > 0) {
                data.domains.forEach(function(domainInfo) {
                    $domainSelect.append($('<option>', {
                        value: domainInfo.sanitized,
                        text: domainInfo.domain
                    }));
                });
            }
        },
        error: function(xhr, status, error) {
            console.error('Error loading domains:', error);
        }
    });
}

function loadLogs() {
    const $loading = $('#loading');
    const $domainSelect = $('#domainSelect');
    const $dateFilter = $('#dateFilter');
    const $container = $('#domainsContainer');
    const $statsDiv = $('#stats');
    
    const selectedDomain = $domainSelect.val();
    const selectedDate = $dateFilter.val();

    if (!selectedDomain || !selectedDate) {
        $container.html(`
            <div class="empty-state">
                <div class="empty-state-icon"><i class="fas fa-inbox"></i></div>
                <h2>Chọn domain và ngày để xem log</h2>
                <p>Vui lòng chọn domain và ngày từ các dropdown ở trên</p>
            </div>
        `);
        $statsDiv.hide();
        return;
    }

    $loading.show();

    const params = new URLSearchParams();
    params.append('domain', selectedDomain);
    params.append('date', selectedDate);
    
    const url = '/api/logs?' + params.toString();
    
    $.ajax({
        url: url,
        method: 'GET',
        dataType: 'json',
        success: function(data) {
            // Update stats
            if (data.stats) {
                $('#totalSuccessful').text(data.stats.total_successful || 0);
                $('#totalFailed').text(data.stats.total_failed || 0);
                $('#retryCount').text(data.stats.retry_count || 0);
            }

            // Render events
            renderEvents(data.events_by_domain || {}, data.failed_events_by_domain || {});
        },
        error: function(xhr, status, error) {
            console.error('Error loading logs:', error);
            let errorMessage = error || 'Unknown error';
            
            $container.html(`
                <div class="empty-state">
                    <div class="empty-state-icon"><i class="fas fa-exclamation-triangle"></i></div>
                    <h2>Lỗi khi tải log</h2>
                    <p>${errorMessage}</p>
                    <p style="margin-top: 12px; font-size: 12px; color: #6c757d;">
                        Kiểm tra console để xem chi tiết lỗi (F12)
                    </p>
                </div>
            `);
            $statsDiv.hide();
        },
        complete: function() {
            $loading.hide();
        }
    });
}

function toggleAutoRefresh() {
    const $checkbox = $('#autoRefresh');
    
    if ($checkbox.is(':checked')) {
        autoRefreshInterval = setInterval(loadLogs, 5000); // Refresh every 5 seconds
    } else {
        if (autoRefreshInterval) {
            clearInterval(autoRefreshInterval);
            autoRefreshInterval = null;
        }
    }
}

// Initialize on page load
$(document).ready(function() {
    // Set default date to today
    const today = new Date().toISOString().split('T')[0];
    $('#dateFilter').val(today);
    
    // Load domains
    loadDomains();
    
    // Load logs when domain or date changes
    $('#domainSelect').on('change', loadLogs);
    $('#dateFilter').on('change', loadLogs);
});
