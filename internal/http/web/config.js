// Config Viewer JavaScript Logic (jQuery)
function loadConfig() {
    const $loading = $('#loading');
    const $container = $('#configContainer');
    const $stats = $('#stats');

    $loading.show();
    $stats.hide();

    $.ajax({
        url: '/api/config',
        method: 'GET',
        dataType: 'json',
        success: function(data) {
            if (!data.routes || data.routes.length === 0) {
                $container.html(`
                    <div class="empty-state">
                        <div class="empty-state-icon"><i class="fas fa-inbox"></i></div>
                        <h2>Không có routes nào được cấu hình</h2>
                        <p>Vui lòng thêm routes vào file config.yaml</p>
                    </div>
                `);
                $stats.hide();
                return;
            }

            $stats.show();
            let totalEndpoints = 0;

            let html = '';
            data.routes.forEach(function(route) {
                // Handle both camelCase (Domain, Endpoints) and lowercase (domain, endpoints)
                const domain = route.domain || route.Domain || 'N/A';
                const endpoints = route.endpoints || route.Endpoints || [];
                const endpointCount = endpoints.length;
                totalEndpoints += endpointCount;

                html += `
                    <div class="route-card">
                        <div class="route-header">
                            <div class="route-domain"><i class="fas fa-globe"></i> ${escapeHtml(domain)}</div>
                            <div class="endpoint-count"><i class="fas fa-server"></i> ${endpointCount} endpoint${endpointCount !== 1 ? 's' : ''}</div>
                        </div>
                        <div class="endpoints-list">
                            ${endpoints.length > 0 
                                ? endpoints.map(function(endpoint) {
                                    return `<div class="endpoint-item"><i class="fas fa-link"></i> ${escapeHtml(endpoint)}</div>`;
                                }).join('')
                                : '<div class="endpoint-item" style="color: #999; font-style: italic;"><i class="fas fa-exclamation-circle"></i> No endpoints configured</div>'
                            }
                        </div>
                    </div>
                `;
            });

            $container.html(html);
            $('#totalRoutes').text(data.count || data.routes.length);
            $('#totalEndpoints').text(totalEndpoints);
        },
        error: function(xhr, status, error) {
            console.error('Error loading config:', error);
            let errorMessage = error || 'Unknown error';
            
            $container.html(`
                <div class="empty-state">
                    <div class="empty-state-icon"><i class="fas fa-exclamation-triangle"></i></div>
                    <h2>Lỗi khi tải cấu hình</h2>
                    <p>${escapeHtml(errorMessage)}</p>
                    <p style="margin-top: 12px; font-size: 12px; color: #6c757d;">
                        Kiểm tra console để xem chi tiết lỗi (F12)
                    </p>
                </div>
            `);
            $stats.hide();
        },
        complete: function() {
            $loading.hide();
        }
    });
}

function reloadConfig() {
    if (!confirm('Bạn có chắc chắn muốn reload config từ file? Các thay đổi sẽ được áp dụng ngay lập tức.')) {
        return;
    }

    $.ajax({
        url: '/api/config/reload',
        method: 'POST',
        dataType: 'json',
        success: function(data) {
            alert('Config đã được reload thành công! Số routes: ' + (data.routes || 0));
            loadConfig(); // Reload to show updated config
        },
        error: function(xhr, status, error) {
            let errorMessage = 'Unknown error';
            if (xhr.responseJSON && xhr.responseJSON.error) {
                errorMessage = xhr.responseJSON.error;
            } else if (xhr.responseText) {
                errorMessage = xhr.responseText;
            }
            alert('Lỗi khi reload config: ' + errorMessage);
        }
    });
}

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

// Initialize on page load
$(document).ready(function() {
    loadConfig();
});
