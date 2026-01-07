# Event Hub Service

A production-ready Golang service that acts as a public HTTP ingress and telephony signal forwarder.

## Features

- **HTTP Ingress**: Accepts telephony signaling events via POST `/events`
- **NATS JetStream Integration**: Publishes events to JetStream for reliable delivery
- **Concurrent Forwarding**: Forwards events to multiple backend endpoints in parallel
- **Automatic Retries**: Leverages JetStream's at-least-once delivery semantics
- **Health Checks**: Exposes GET `/health` endpoint
- **Web Dashboard**: Real-time monitoring interface for events, statistics, and logs
- **Domain-based Logging**: Logs grouped by domain with automatic rotation
- **Event Tracking**: In-memory store for successful and failed events
- **Log File Reading**: Read and parse events from log files via API
- **Hot Reload Config**: Automatically reload route configuration without restarting
- **Graceful Shutdown**: Handles SIGINT/SIGTERM cleanly

## Architecture

```
HTTP Request → POST /events → NATS JetStream → Consumer → Forward to Backends
```

1. Events are received via HTTP POST and validated
2. Valid events are published to NATS JetStream (no blocking)
3. A PUSH-based consumer receives messages from JetStream
4. Events are forwarded to ALL configured endpoints concurrently
5. Message is acknowledged only if ALL endpoints succeed
6. If ANY endpoint fails, JetStream redelivers the entire message

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

Accepts telephony signaling events.

**Request Body:**
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
- `domain`: Used for routing to backend endpoints (required)

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

Reads events from log files, grouped by domain.

**Query Parameters:**
- `domain`: Domain name (sanitized, e.g., `example_com` for `example.com`) (optional, if omitted returns list of domains)
- `date`: Date in format `YYYY-MM-DD` (default: today)

**Response (with domain):**
```json
{
  "events_by_domain": {
    "example.com": [...]
  },
  "failed_events_by_domain": {
    "example.com": [
      {
        "call_id": "123",
        "domain": "example.com",
        "direction": "inbound",
        "state": "missed",
        "status": "busy-line",
        "failed_at": "2026-01-04T10:00:00Z",
        "error": "connection timeout",
        "delivery_attempt": 1,
        "max_deliveries": 3,
        "will_retry": true
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

Web dashboard for monitoring events, statistics, and logs.

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
- **Domain-based Routing**: Events are routed based on the `domain` field in the payload
- **Delivery Attempt Tracking**: Each forwarded event includes `delivery_attempt` in the payload (1, 2, 3...)
- **Event Tracking**: Successful and failed events are stored in-memory and can be queried via API

## Logging

Structured logging using zap with domain-based file organization.

### Log Structure

Logs include:
- `call_id`: Unique call identifier
- `domain`: Tenant identifier (used for routing)
- `direction`: Call direction (e.g., "inbound", "outbound")
- `state`: Call state (e.g., "missed", "answered")
- `status`: Call status (e.g., "busy-line", "completed")
- `delivery_attempt`: Current delivery attempt (1, 2, or 3)
- Error details when forwarding fails

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

### Log Events

The following events are logged:
- `Event received and published`: When an event is received via HTTP and published to NATS
- `Forwarding event`: When forwarding to backend endpoints begins
- `Event forwarded successfully`: When all endpoints respond successfully
- `Failed to forward event`: When forwarding fails (with error details and `delivery_attempt`)

### Reading Logs

Logs can be read via:
- **API**: `/api/logs?domain=<sanitized_domain>&date=YYYY-MM-DD`
- **Dashboard**: Web interface with toggle to read from log files
- **File System**: Direct access to log files in `logs/` directory

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

## Web Dashboard

The service includes a web dashboard accessible at `http://localhost:8080/` (or your configured port).

### Features

- **Event Monitoring**: View successful and failed events grouped by domain
- **Statistics**: Real-time statistics (total successful, failed, retries, domain counts)
- **Filtering**: Filter events by domain and type (successful/failed/all)
- **Log File Reading**: Toggle between in-memory store and log files
- **Date Selection**: Select specific date when reading from log files
- **Retry Status**: Visual indicators for events that will be retried
- **Event Details**: Expandable event cards with full payload information

### Usage

1. Start the service with domain logging enabled:
   ```bash
   ./cmd/app -config config.yaml -log-level info -domain-logging
   ```

2. Open browser to `http://localhost:8080/`

3. Use the toggle "Đọc từ log files" to switch between:
   - **In-memory store**: Real-time events from current session
   - **Log files**: Historical events from log files

4. Select a date when reading from log files

5. Filter by domain using the domain selector

## Project Structure

```
telephony-forwarder/
├── cmd/
│   └── main.go              # Application entry point
├── internal/
│   ├── config/              # Configuration management
│   ├── consumer/            # Event consumer service
│   ├── forwarder/           # HTTP forwarding logic
│   ├── http/                # HTTP handlers and web dashboard
│   │   └── web/
│   │       └── dashboard.html  # Embedded web dashboard
│   ├── logger/              # Structured logging with domain-based files
│   ├── nats/                # NATS publisher and consumer
│   └── store/               # In-memory event store
├── logs/                    # Domain-based log files (created at runtime)
│   ├── example_com/
│   │   └── YYYY-MM-DD.log
│   └── ...
├── config.yaml              # Configuration file
├── go.mod
└── README.md
```

