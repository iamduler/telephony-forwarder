# Event Hub Service

A production-ready Golang service that acts as a public HTTP ingress and telephony signal forwarder.

## Features

- **HTTP Ingress**: Accepts telephony signaling events via POST `/events`
- **NATS JetStream Integration**: Publishes events to JetStream for reliable delivery
- **Concurrent Forwarding**: Forwards events to multiple backend endpoints in parallel
- **Automatic Retries**: Leverages JetStream's at-least-once delivery semantics
- **Health Checks**: Exposes GET `/health` endpoint
- **Web Dashboard**: Real-time monitoring interface for events, statistics, and logs
- **Log Viewer**: Standalone interface for viewing historical logs with domain and date selection
- **Config Viewer**: Web interface to view and manage current route configuration
- **Domain-based Logging**: Logs grouped by domain with automatic rotation
- **Event Tracking**: In-memory store for successful and failed events
- **Full Event Data Logging**: Preserves all fields from different PBX systems
- **Local Timezone**: Logs use local timezone instead of UTC
- **Hot Reload Config**: Automatically reload route configuration without restarting
- **Multi-PBX Support**: Handles events from different PBX systems with varying field structures
- **Graceful Shutdown**: Handles SIGINT/SIGTERM cleanly

## Architecture

```
HTTP Request → POST /events → NATS JetStream → Consumer → Forward to Backends
```

1. Events are received via HTTP POST and validated
2. Valid events are published to NATS JetStream (no blocking)
3. A durable consumer receives messages from JetStream (preserves position across restarts)
4. Events are forwarded to ALL configured endpoints concurrently
5. Message is acknowledged only if ALL endpoints succeed
6. If ANY endpoint fails, JetStream redelivers the entire message

**Note**: The consumer is durable and preserves its position. If a consumer already exists, it will be reused instead of being recreated, ensuring no messages are lost during application restarts.

## JetStream Retry and Backoff Behavior

The service relies entirely on JetStream's built-in retry mechanism:

- **AckWait**: 10 seconds (configurable, must be > backend timeout of 3 seconds)
- **MaxDeliveries**: 3 attempts total
- **AckPolicy**: Explicit - messages must be manually acknowledged
- **No Application-Level Retries**: The service does NOT implement retry logic

### How It Works

1. When an event is received, it's published to JetStream
2. The consumer receives the message (PUSH-based delivery)
3. The service forwards to ALL endpoints concurrently
4. If ALL endpoints return 2xx, the message is acknowledged
5. If ANY endpoint fails (non-2xx or timeout), the message is NOT acknowledged
6. After `ack_wait` seconds, JetStream automatically redelivers the message
7. This process repeats up to `max_deliveries` times

**Important**: Backend endpoints MUST be idempotent based on `event_id` since the same event may be delivered multiple times.

## Configuration

Create a `config.yaml` file (see `config.yaml.example`):

```yaml
server:
  port: 8080

nats:
  url: "nats://localhost:4222"
  stream_name: "call-signals"
  subject_pattern: "call.signal.*"
  ack_wait_seconds: 10
  max_deliveries: 3

routes:
  - domain: "telephony-forwarder.com"
    endpoints:
      - "https://backend1.example.com/webhook"
      - "https://backend2.example.com/webhook"
```

### Hot Reload Configuration

The application supports hot reloading of route configuration without restarting:

#### Automatic Reload (File Watcher)

- The application automatically watches the config file for changes
- When you modify `config.yaml`, changes are detected within 2 seconds
- Only the `routes` section is reloaded automatically
- No restart required - just save the file!

**Example:**
```bash
# Edit config.yaml to add/remove/modify routes
vim config.yaml

# Save the file - changes are automatically applied within 2 seconds
# Check logs to confirm reload:
# {"level":"info","msg":"Config auto-reloaded successfully","route_count":3}
```

#### Manual Reload via API

You can also trigger a reload manually via API:

```bash
curl -X POST http://localhost:8080/api/config/reload
```

**Response:**
```json
{
  "status": "success",
  "message": "Configuration reloaded successfully",
  "routes": 2
}
```

#### What Gets Reloaded

✅ **Reloaded automatically:**
- `routes` section (domain → endpoints mapping)
- Adding new domains
- Removing domains
- Modifying endpoints for existing domains

❌ **Requires restart:**
- `server` configuration (port, timeouts)
- `nats` configuration (URL, stream name, subject pattern, ack_wait, max_deliveries)

#### Reload Behavior

- **Thread-safe**: Config updates are atomic and thread-safe
- **Validation**: Config is validated before applying changes
- **Error handling**: Invalid configs are rejected, old config remains active
- **Logging**: All reload events are logged with route count

## Building

```bash
go mod download
go build -o telephony-forwarder ./cmd/main.go
```

## Deployment

The project includes a deployment script (`deploy.py`) for automated deployment:

### Automated Deployment Script

The `deploy.py` script automates the deployment process:

**Features:**
- **Git Integration**: Automatically checks for new commits from remote repository
- **Smart Build**: Only rebuilds if there are new commits (unless `--force` is used)
- **Service Management**: Automatically restarts the systemd service after build
- **Logging**: Logs all deployment activities to `/var/log/telephony-forwarder/deploy.log`

**Usage:**

```bash
# Basic deployment (checks for new commits, builds, and restarts service)
python3 deploy.py

# Force rebuild even if no new commits
python3 deploy.py --force

# Build only, do not restart service
python3 deploy.py --no-restart

# Combine flags
python3 deploy.py --force --no-restart
```

**Configuration:**

The script uses the following configuration (edit `deploy.py` to customize):
- `PROJECT_DIR`: Project directory path (default: `/root/telephony-forwarder`)
- `SERVICE_NAME`: Systemd service name (default: `telephony-forwarder`)
- `BUILD_CMD`: Go build command (default: `["go", "build", "-o", "app", "./cmd"]`)
- `LOG_FILE`: Deployment log file path (default: `/var/log/telephony-forwarder/deploy.log`)

**How It Works:**

1. Checks for new commits from remote repository using `git fetch` and `git status`
2. If new commits are found (or `--force` is used):
   - Pulls latest code from repository
   - Builds the Go application
   - Restarts the systemd service (unless `--no-restart` is used)
3. Logs all operations to the deployment log file

**Prerequisites:**

- Python 3.x
- Git repository initialized
- Systemd service configured
- Proper permissions to restart service

**Example Output:**

```
📥 New commits detected → pulling...
$ git pull
...
$ go build -o app ./cmd
...
$ systemctl restart telephony-forwarder
🚀 Deploy completed successfully
```

## Running

```bash
./telephony-forwarder -config config.yaml -log-level info
```

### Command Line Flags

- `-config`: Path to configuration file (default: `config.yaml`)
- `-log-level`: Log level: debug, info, warn, error (default: `info`)
- `-log-file`: Path to log file (empty = stdout only, ignored if `-domain-logging` is enabled)
- `-domain-logging`: Enable domain-based logging (logs grouped by domain in `logs/` directory) (default: `true`)

## API Endpoints

### POST /events

Accepts telephony signaling events from different PBX systems.

**Request Body:**
The service accepts any JSON structure. All fields are preserved and logged. Example:
```json
{
  "actual_hotline": "",
  "billsec": "62",
  "call_id": "d1570d38-edc3-4751-a32d-63a30e95c57a",
  "crm_contact_id": "",
  "direction": "inbound",
  "domain": "vietanh.cloudgo.vn",
  "duration": "63",
  "from_number": "0914315989",
  "hotline": "02743857008",
  "network": "vina",
  "provider": "",
  "receive_dest": "2006",
  "sip_call_id": "7bcP02218160402mbeGhEfCjIjJ0m@10.202.49.38",
  "sip_hangup_disposition": "recv_bye",
  "state": "missed",
  "status": "busy-line",
  "time_ended": "2026-01-04 16:19:14",
  "time_started": "2026-01-04 16:18:12",
  "to_number": ""
}
```

**Required Fields:**
- `domain` or `Domain`: Used for routing to backend endpoints (required, case-insensitive)

**Multi-PBX Support:**
- The service preserves **ALL fields** from incoming JSON, regardless of structure
- Supports different naming conventions (camelCase, snake_case, etc.)
- Field names are normalized where possible (e.g., `Domain` → `domain`)
- All event data is logged in full for later inspection

**Response:**
- `202 Accepted`: Event accepted and published to JetStream
- `400 Bad Request`: Invalid payload or missing `domain` field
- `500 Internal Server Error`: Failed to publish to JetStream

### GET /health

Health check endpoint.

**Response:**
- `200 OK`: Service is healthy (HTTP server running, NATS connected)
- `503 Service Unavailable`: NATS not connected

### GET /api/events

Returns events from the in-memory store, grouped by domain.

**Query Parameters:**
- `type`: Filter by event type: `successful`, `failed`, or `all` (default: `all`)
- `domain`: Filter by domain (optional)

**Response:**
```json
{
  "events_by_domain": {
    "example.com": [
      {
        "call_id": "123",
        "domain": "example.com",
        "direction": "inbound",
        "state": "missed",
        "status": "busy-line",
        "forwarded_at": "2026-01-04T10:00:00Z",
        "delivery_attempt": 1,
        "event": {...}
      }
    ]
  },
  "failed_events_by_domain": {
    "example.com": [
      {
        "call_id": "456",
        "domain": "example.com",
        "direction": "inbound",
        "state": "missed",
        "status": "busy-line",
        "failed_at": "2026-01-04T10:00:00Z",
        "error": "connection timeout",
        "delivery_attempt": 1,
        "max_deliveries": 3,
        "will_retry": true,
        "event": {...}
      }
    ]
  }
}
```

### GET /api/stats

Returns statistics about forwarded events.

**Response:**
```json
{
  "total_successful": 100,
  "total_failed": 5,
  "retry_count": 3,
  "successful_domain_count": 10,
  "failed_domain_count": 2
}
```

### GET /api/logs

Reads events from log files, grouped by domain. Returns **full event data** with all fields preserved.

**Query Parameters:**
- `domain`: Domain name (sanitized, e.g., `example_com` for `example.com`) (optional, if omitted returns list of domains)
- `date`: Date in format `YYYY-MM-DD` (default: today)

**Response (with domain):**
```json
{
  "events_by_domain": {
    "example.com": [
      {
        "call_id": "123",
        "domain": "example.com",
        "direction": "inbound",
        "state": "missed",
        "status": "busy-line",
        "actual_hotline": "",
        "billsec": "62",
        "duration": "63",
        "from_number": "0914315989",
        "hotline": "02743857008",
        "network": "vina",
        "sip_call_id": "...",
        "time_ended": "2026-01-04 16:19:14",
        "time_started": "2026-01-04 16:18:12",
        "forwarded_at": "2026-01-18T09:03:44.130+07:00",
        "timestamp": "2026-01-18T09:03:44.130+07:00",
        "delivery_attempt": 1,
        "msg": "Event forwarded successfully"
      }
    ]
  },
  "failed_events_by_domain": {
    "example.com": [
      {
        "call_id": "123",
        "domain": "example.com",
        "direction": "inbound",
        "state": "missed",
        "status": "busy-line",
        "failed_at": "2026-01-18T09:03:44.130+07:00",
        "timestamp": "2026-01-18T09:03:44.130+07:00",
        "error": "connection timeout",
        "delivery_attempt": 1,
        "max_deliveries": 3,
        "will_retry": true,
        "msg": "Event forwarding failed"
      }
    ]
  },
  "stats": {
    "total_successful": 100,
    "total_failed": 5,
    "retry_count": 3
  }
}
```

**Note**: The response includes **all fields** from the original event payload, not just a subset. Timestamps are converted to local timezone.

**Response (without domain):**
```json
{
  "domains": [
    {
      "domain": "example.com",
      "sanitized": "example_com",
      "dates": ["2026-01-04", "2026-01-03"]
    }
  ]
}
```

### GET /api/logs/domains

Lists all available log domains.

**Response:**
```json
{
  "domains": [
    {
      "domain": "example.com",
      "sanitized": "example_com",
      "dates": ["2026-01-04", "2026-01-03"]
    }
  ]
}
```

### GET /api/stream/messages

Reads messages directly from the NATS JetStream stream.

**Query Parameters:**
- `limit`: Maximum number of messages to return (default: 50)

**Response:**
```json
{
  "messages": [
    {
      "subject": "call.signal.example.com",
      "sequence": 123,
      "timestamp": "2026-01-04T10:00:00Z",
      "data": {...}
    }
  ],
  "total": 123
}
```

### GET /

Web dashboard for monitoring real-time events and statistics from in-memory store.

### GET /logs

Standalone log viewer interface for viewing historical logs from log files.

### GET /api/config

Returns the current route configuration.

**Response:**
```json
{
  "routes": [
    {
      "domain": "example.com",
      "endpoints": [
        "https://backend1.example.com/webhook",
        "https://backend2.example.com/webhook"
      ]
    }
  ],
  "count": 1
}
```

### GET /config

Web interface for viewing and managing route configuration. Displays:
- All configured routes with domains and endpoints
- Total routes and endpoints count
- Option to reload configuration from file

### POST /api/config/reload

Reloads the configuration file (routes mapping) without restarting the application.

**Response:**
```json
{
  "status": "success",
  "message": "Configuration reloaded successfully",
  "routes": 2
}
```

**Note**: Only the `routes` section (domain mapping) is reloaded. Changes to `server` or `nats` configuration require a restart.

**Error Response:**
- `400 Bad Request`: Invalid configuration file
- `500 Internal Server Error`: Failed to reload config

## Event Forwarding

Events are forwarded to ALL endpoints configured for the domain:

- **Concurrent**: All endpoints receive the request in parallel
- **Atomic**: Either ALL endpoints succeed or the message is redelivered
- **Timeout**: 3 seconds per endpoint
- **Idempotent**: Backends must handle duplicate events (same `call_id`)
- **Domain-based Routing**: Events are routed based on the `domain` field in the payload (case-insensitive)
- **Delivery Attempt Tracking**: Each forwarded event includes `delivery_attempt` in the payload (1, 2, 3...)
- **Event Tracking**: Successful and failed events are stored in-memory and can be queried via API
- **Full Data Preservation**: All fields from the original event are preserved and forwarded to backends
- **Multi-PBX Support**: Handles events from different PBX systems with varying field structures and naming conventions

## Logging

Structured logging using zap with domain-based file organization.

### Log Structure

Logs include **all fields** from the original event payload, preserving complete data from different PBX systems:

- **Event Data**: All fields from the incoming event (call_id, domain, direction, state, status, and any custom fields)
- `delivery_attempt`: Current delivery attempt (1, 2, or 3)
- `msg`: Log message (e.g., "Event received and published", "Forwarding event", "Event forwarded successfully", "Event forwarding failed")
- `timestamp`: Timestamp in local timezone (format: `2006-01-02T15:04:05.000Z07:00`)
- Error details when forwarding fails

**Example log entry:**
```json
{
  "level": "info",
  "timestamp": "2026-01-18T09:03:44.130+07:00",
  "msg": "Event forwarded successfully",
  "domain": "example.com",
  "event": {
    "call_id": "123",
    "domain": "example.com",
    "direction": "inbound",
    "state": "missed",
    "status": "busy-line",
    "actual_hotline": "",
    "billsec": "62",
    "duration": "63",
    "from_number": "0914315989",
    "hotline": "02743857008",
    "network": "vina",
    "sip_call_id": "...",
    "time_ended": "2026-01-04 16:19:14",
    "time_started": "2026-01-04 16:18:12",
    "delivery_attempt": 1
  }
}
```

### Domain-based Logging

When `-domain-logging` is enabled (default), logs are organized as follows:

```
logs/
├── example_com/
│   ├── 2026-01-04.log
│   ├── 2026-01-03.log
│   └── ...
├── another_domain_com/
│   ├── 2026-01-04.log
│   └── ...
└── unknown_domain_com/
    └── 2026-01-04.log
```

**Features:**
- **Automatic Rotation**: Log files rotate daily (one file per day per domain)
- **Size Limits**: 500MB per file, 30 backups, 30 days retention
- **Compression**: Old log files are automatically compressed
- **Domain Sanitization**: Domain names are sanitized for filesystem compatibility (e.g., `example.com` → `example_com`)
- **Local Timezone**: All timestamps are stored in local timezone (not UTC)
- **Full Data Preservation**: All fields from event payload are logged, supporting different PBX systems

### Log Events

The following events are logged with full event data:
- `Event received and published`: When an event is received via HTTP and published to NATS (includes full event payload)
- `Forwarding event`: When forwarding to backend endpoints begins (includes full event payload with `delivery_attempt`)
- `Event forwarded successfully`: When all endpoints respond successfully (includes full event payload)
- `Event forwarding failed`: When forwarding fails (includes full event payload, error details, and `delivery_attempt`)

All log entries preserve the complete event data structure, allowing full reconstruction of events from logs.

### Reading Logs

Logs can be read via:
- **API**: `/api/logs?domain=<sanitized_domain>&date=YYYY-MM-DD` - Returns full event data with all fields
- **Log Viewer**: Standalone web interface at `/logs` for viewing historical logs
- **Dashboard**: Web interface at `/` for real-time events from in-memory store
- **File System**: Direct access to log files in `logs/` directory (JSON format, one entry per line)

### Log Analysis and Debugging

The service provides tools and methods to analyze logs and debug issues:

#### Using the Log Analysis Script

A helper script `check_received_events.sh` is provided to analyze events received from PBX systems and compare them with forwarding events:

**Basic Usage:**
```bash
# View all events received for a domain today
./check_received_events.sh "" "vietanh.cloudgo.vn"

# View events for a specific call_id
./check_received_events.sh "your-call-id-here" "vietanh.cloudgo.vn"

# View events for a specific date
./check_received_events.sh "" "vietanh.cloudgo.vn" "2026-01-18"
```

**What the Script Shows:**
- ✅ **Events Received**: All events received from PBX (log "Event received and published")
- ➡️ **Events Forwarded**: All forwarding attempts (log "Forwarding event")
- 📊 **Statistics**: Total counts and analysis
- ⚠️ **Warnings**: Alerts if more forwarding events than received events (indicates duplicate processing)

**Example Output:**
```
==========================================
Checking events received for domain: vietanh.cloudgo.vn
Date: 2026-01-18
Call ID: abc123
==========================================

📋 Events RECEIVED from PBX (before forwarding):
----------------------------------------
  ✅ 2026-01-18T10:00:00.000+07:00 | call_id: abc123 | domain: vietanh.cloudgo.vn
  ✅ 2026-01-18T10:00:05.000+07:00 | call_id: abc123 | domain: vietanh.cloudgo.vn

📊 Total events received: 2

📤 Events FORWARDED to endpoints:
----------------------------------------
  ➡️  2026-01-18T10:00:01.000+07:00 | call_id: abc123 | attempt: 1 | endpoints: 2
  ➡️  2026-01-18T10:00:06.000+07:00 | call_id: abc123 | attempt: 1 | endpoints: 2

📊 Total forwarding events: 2

🔍 Analysis for call_id: abc123
----------------------------------------
  Events received: 2
  Events forwarded: 2
  
  ✅ Normal: Each received event was forwarded once
  With 2 endpoints, this means 4 HTTP requests were made
```

#### Manual Log Analysis

**View Events Received from PBX:**
```bash
# View all events received for a domain today
grep "Event received and published" logs/vietanh_cloudgo_vn/$(date +%Y-%m-%d).log

# Count events received
grep -c "Event received and published" logs/vietanh_cloudgo_vn/$(date +%Y-%m-%d).log

# View events for a specific call_id
grep "Event received and published" logs/vietanh_cloudgo_vn/$(date +%Y-%m-%d).log | grep "your-call-id-here"
```

**View Forwarding Events:**
```bash
# View all forwarding events
grep "Forwarding event" logs/vietanh_cloudgo_vn/$(date +%Y-%m-%d).log

# Count forwarding events
grep -c "Forwarding event" logs/vietanh_cloudgo_vn/$(date +%Y-%m-%d).log

# Compare received vs forwarded
echo "Events received: $(grep -c 'Event received and published' logs/vietanh_cloudgo_vn/$(date +%Y-%m-%d).log)"
echo "Events forwarded: $(grep -c 'Forwarding event' logs/vietanh_cloudgo_vn/$(date +%Y-%m-%d).log)"
```

**Real-time Log Monitoring:**
```bash
# Monitor logs in real-time
tail -f logs/vietanh_cloudgo_vn/$(date +%Y-%m-%d).log | grep -E "(Event received and published|Forwarding event)"
```

**Debugging Duplicate Forwarding Issues:**
```bash
# Check if there are more forwarding events than received events
RECEIVED=$(grep -c 'Event received and published' logs/vietanh_cloudgo_vn/$(date +%Y-%m-%d).log)
FORWARDED=$(grep -c 'Forwarding event' logs/vietanh_cloudgo_vn/$(date +%Y-%m-%d).log)

if [ "$FORWARDED" -gt "$RECEIVED" ]; then
    echo "⚠️  WARNING: More forwarding events ($FORWARDED) than received events ($RECEIVED)"
    echo "This suggests duplicate processing or multiple instances running."
fi
```

#### Key Log Messages

The following log messages are important for debugging:

1. **"Event received and published"**: Event received from PBX via HTTP POST (before forwarding)
   - Contains: `call_id`, `domain`, and full event data
   - This log is written when the event is successfully published to NATS JetStream
   - Use this to verify how many events were actually received from the PBX system

2. **"Forwarding event"**: Starting to forward event to endpoints
   - Contains: `call_id`, `domain`, `delivery_attempt`, `endpoint_count`, and full event data
   - This log is written when the consumer starts forwarding to backend endpoints

3. **"Event forwarded successfully"**: All endpoints responded successfully
   - Contains: `call_id`, `domain`, `endpoint_count`, and full event data

4. **"Event forwarding failed"**: Forwarding failed (one or more endpoints failed)
   - Contains: `call_id`, `domain`, `delivery_attempt`, error details, and full event data

#### Debugging Workflow

When investigating duplicate forwarding issues:

1. **Check how many events were received:**
   ```bash
   ./check_received_events.sh "call-id" "domain"
   ```

2. **Compare with forwarding events:**
   - If received 1 event but forwarded 3 times → Check for multiple instances running
   - If received 3 events and forwarded 3 times → Normal (each event forwarded once to 2 endpoints = 6 HTTP requests)

3. **Check for multiple instances:**
   ```bash
   ps aux | grep -E "(cmd/app|telephony-forwarder)" | grep -v grep
   ```

4. **Check NATS consumers:**
   ```bash
   # If you have nats CLI installed
   nats consumer ls call-signals
   ```

## Graceful Shutdown

The service handles SIGINT and SIGTERM:

1. Stops accepting new HTTP requests
2. Stops consuming new messages from JetStream
3. Waits for in-flight message processing to complete
4. Closes HTTP server
5. Closes NATS connections

## Requirements

- Go 1.21+
- NATS Server with JetStream enabled
- Backend endpoints must be idempotent

## Web Interfaces

The service includes three web interfaces:

### Dashboard (`/`)

Real-time monitoring interface for current session events.

**Features:**
- **Event Monitoring**: View successful and failed events grouped by domain
- **Statistics**: Real-time statistics (total successful, failed, retries, domain counts)
- **Filtering**: Filter events by domain and type (successful/failed/all)
- **Auto-refresh**: Optional automatic refresh every 5 seconds
- **Event Details**: Expandable event cards with full payload information
- **Retry Status**: Visual indicators for events that will be retried

**Usage:**
1. Start the service:
   ```bash
   ./cmd/app -config config.yaml -log-level info -domain-logging
   ```

2. Open browser to `http://localhost:8080/`

3. Filter by domain using the domain filter input

4. Toggle auto-refresh for real-time updates

### Log Viewer (`/logs`)

Standalone interface for viewing historical logs from log files.

**Features:**
- **Domain Selection**: Dropdown to select domain (auto-populated from available logs)
- **Date Selection**: Date picker to select specific date (default: today)
- **Raw JSON Display**: Shows complete event data as raw JSON with all fields
- **Message Display**: Shows log message (e.g., "Event forwarded successfully") in event header
- **Auto-refresh**: Optional automatic refresh every 5 seconds
- **Local Timezone**: Timestamps displayed in local timezone
- **Full Data**: Displays all fields from event payload, preserving data from different PBX systems

**Usage:**
1. Open browser to `http://localhost:8080/logs`

2. Select domain from dropdown

3. Select date (default: today)

4. Events are automatically loaded and displayed as raw JSON

5. Toggle auto-refresh to automatically reload logs every 5 seconds

**Note**: The log viewer displays events as raw JSON to preserve all fields from different PBX systems. All timestamps are converted to local timezone for display.

### Config Viewer (`/config`)

Web interface for viewing and managing route configuration.

**Features:**
- **Route Display**: View all configured routes with domains and endpoints
- **Statistics**: Display total routes and total endpoints count
- **Reload Config**: Button to manually reload configuration from file
- **Refresh**: Button to refresh the configuration view
- **Navigation**: Links to Dashboard and Log Viewer

**Usage:**
1. Open browser to `http://localhost:8080/config`

2. View all configured routes with their endpoints

3. Click "Reload Config" to reload configuration from file (requires confirmation)

4. Click "Refresh" to reload the view

**Note**: The config viewer shows the current in-memory configuration. After reloading config, click "Refresh" to see the updated routes.

## Project Structure

```
telephony-forwarder/
├── cmd/
│   └── main.go              # Application entry point
├── internal/
│   ├── config/              # Configuration management
│   ├── consumer/            # Event consumer service
│   ├── forwarder/           # HTTP forwarding logic
│   ├── http/                # HTTP handlers and web interfaces
│   │   └── web/
│   │       ├── dashboard.html  # Embedded web dashboard
│   │       ├── dashboard.js    # Dashboard JavaScript (jQuery)
│   │       ├── logs.html       # Log viewer interface
│   │       ├── logs.js         # Log viewer JavaScript (jQuery)
│   │       ├── config.html     # Config viewer interface
│   │       └── config.js      # Config viewer JavaScript (jQuery)
│   ├── logger/              # Structured logging with domain-based files
│   ├── nats/                # NATS publisher and consumer
│   └── store/               # In-memory event store
├── logs/                    # Domain-based log files (created at runtime)
│   ├── example_com/
│   │   └── YYYY-MM-DD.log
│   └── ...
├── config.yaml              # Configuration file
├── deploy.py                # Automated deployment script
├── check_received_events.sh # Log analysis helper script
├── go.mod
└── README.md
```

