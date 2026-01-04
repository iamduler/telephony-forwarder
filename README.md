# Event Hub Service

A production-ready Golang service that acts as a public HTTP ingress and telephony signal forwarder.

## Features

- **HTTP Ingress**: Accepts telephony signaling events via POST `/events`
- **NATS JetStream Integration**: Publishes events to JetStream for reliable delivery
- **Concurrent Forwarding**: Forwards events to multiple backend endpoints in parallel
- **Automatic Retries**: Leverages JetStream's at-least-once delivery semantics
- **Health Checks**: Exposes GET `/health` endpoint
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

## Event Forwarding

Events are forwarded to ALL endpoints configured for the domain:

- **Concurrent**: All endpoints receive the request in parallel
- **Atomic**: Either ALL endpoints succeed or the message is redelivered
- **Timeout**: 3 seconds per endpoint
- **Idempotent**: Backends must handle duplicate events (same `call_id`)
- **Domain-based Routing**: Events are routed based on the `domain` field in the payload

## Logging

Structured logging using zap. Logs include:
- `call_id`: Unique call identifier
- `domain`: Tenant identifier (used for routing)
- `state`: Call state (e.g., "missed", "answered")
- `status`: Call status (e.g., "busy-line", "completed")
- `delivery_attempt`: Current delivery attempt (1, 2, or 3)
- Error details when forwarding fails

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

## Project Structure

```
calleventhub/
├── cmd/
│   └── main.go              # Application entry point
├── internal/
│   ├── config/              # Configuration management
│   ├── consumer/            # Event consumer service
│   ├── forwarder/           # HTTP forwarding logic
│   ├── http/                # HTTP handlers
│   ├── logger/              # Structured logging
│   └── nats/                # NATS publisher and consumer
├── config.yaml.example      # Example configuration
├── go.mod
└── README.md
```

