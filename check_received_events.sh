#!/bin/bash
# Script to check how many events were received from PBX before forwarding
# Usage: ./check_received_events.sh [call_id] [domain] [date]

CALL_ID="${1:-}"
DOMAIN="${2:-vietanh.cloudgo.vn}"
DATE="${3:-$(date +%Y-%m-%d)}"

echo "=========================================="
echo "Checking events received for domain: $DOMAIN"
echo "Date: $DATE"
if [ -n "$CALL_ID" ]; then
    echo "Call ID: $CALL_ID"
fi
echo "=========================================="
echo ""

# Sanitize domain for log file path
SAFE_DOMAIN=$(echo "$DOMAIN" | tr '.' '_' | tr '/' '_' | tr '\\' '_' | tr ':' '_' | tr '*' '_' | tr '?' '_' | tr '"' '_' | tr '<' '_' | tr '>' '_' | tr '|' '_')
LOG_FILE="logs/${SAFE_DOMAIN}/${DATE}.log"

if [ ! -f "$LOG_FILE" ]; then
    echo "❌ Log file not found: $LOG_FILE"
    exit 1
fi

echo "📋 Events RECEIVED from PBX (before forwarding):"
echo "----------------------------------------"
if [ -n "$CALL_ID" ]; then
    # Filter by call_id
    grep -a "Event received and published" "$LOG_FILE" | grep -a "\"call_id\":\"$CALL_ID\"" | while IFS= read -r line; do
        # Extract timestamp and call_id from JSON log
        timestamp=$(echo "$line" | grep -o '"timestamp":"[^"]*"' | cut -d'"' -f4)
        call_id=$(echo "$line" | grep -o '"call_id":"[^"]*"' | cut -d'"' -f4)
        domain=$(echo "$line" | grep -o '"domain":"[^"]*"' | cut -d'"' -f4)
        echo "  ✅ $timestamp | call_id: $call_id | domain: $domain"
    done
    COUNT=$(grep -a "Event received and published" "$LOG_FILE" | grep -a "\"call_id\":\"$CALL_ID\"" | wc -l)
else
    # Show all events
    grep -a "Event received and published" "$LOG_FILE" | while IFS= read -r line; do
        # Extract timestamp and call_id from JSON log
        timestamp=$(echo "$line" | grep -o '"timestamp":"[^"]*"' | cut -d'"' -f4)
        call_id=$(echo "$line" | grep -o '"call_id":"[^"]*"' | cut -d'"' -f4)
        domain=$(echo "$line" | grep -o '"domain":"[^"]*"' | cut -d'"' -f4)
        echo "  ✅ $timestamp | call_id: $call_id | domain: $domain"
    done
    COUNT=$(grep -a "Event received and published" "$LOG_FILE" | wc -l)
fi

echo ""
echo "📊 Total events received: $COUNT"
echo ""

echo "📤 Events FORWARDED to endpoints:"
echo "----------------------------------------"
if [ -n "$CALL_ID" ]; then
    # Filter by call_id
    grep -a "Forwarding event" "$LOG_FILE" | grep -a "\"call_id\":\"$CALL_ID\"" | while IFS= read -r line; do
        # Extract timestamp, call_id, delivery_attempt, and endpoint_count
        timestamp=$(echo "$line" | grep -o '"timestamp":"[^"]*"' | cut -d'"' -f4)
        call_id=$(echo "$line" | grep -o '"call_id":"[^"]*"' | cut -d'"' -f4)
        attempt=$(echo "$line" | grep -o '"delivery_attempt":[0-9]*' | cut -d':' -f2)
        endpoints=$(echo "$line" | grep -o '"endpoint_count":[0-9]*' | cut -d':' -f2)
        echo "  ➡️  $timestamp | call_id: $call_id | attempt: $attempt | endpoints: $endpoints"
    done
    FORWARD_COUNT=$(grep -a "Forwarding event" "$LOG_FILE" | grep -a "\"call_id\":\"$CALL_ID\"" | wc -l)
else
    # Show all forwarding events
    grep -a "Forwarding event" "$LOG_FILE" | while IFS= read -r line; do
        # Extract timestamp, call_id, delivery_attempt, and endpoint_count
        timestamp=$(echo "$line" | grep -o '"timestamp":"[^"]*"' | cut -d'"' -f4)
        call_id=$(echo "$line" | grep -o '"call_id":"[^"]*"' | cut -d'"' -f4)
        attempt=$(echo "$line" | grep -o '"delivery_attempt":[0-9]*' | cut -d':' -f2)
        endpoints=$(echo "$line" | grep -o '"endpoint_count":[0-9]*' | cut -d':' -f2)
        echo "  ➡️  $timestamp | call_id: $call_id | attempt: $attempt | endpoints: $endpoints"
    done
    FORWARD_COUNT=$(grep -a "Forwarding event" "$LOG_FILE" | wc -l)
fi

echo ""
echo "📊 Total forwarding events: $FORWARD_COUNT"
echo ""

if [ -n "$CALL_ID" ]; then
    echo "🔍 Analysis for call_id: $CALL_ID"
    echo "----------------------------------------"
    RECEIVED=$(grep -a "Event received and published" "$LOG_FILE" | grep -a "\"call_id\":\"$CALL_ID\"" | wc -l)
    FORWARDED=$(grep -a "Forwarding event" "$LOG_FILE" | grep -a "\"call_id\":\"$CALL_ID\"" | wc -l)
    echo "  Events received: $RECEIVED"
    echo "  Events forwarded: $FORWARDED"
    
    if [ "$FORWARDED" -gt "$RECEIVED" ]; then
        echo ""
        echo "  ⚠️  WARNING: More forwarding events than received events!"
        echo "  This suggests duplicate processing or multiple instances running."
        echo "  Expected: $RECEIVED received → $RECEIVED forwarding (with 2 endpoints = $((RECEIVED * 2)) HTTP requests)"
        echo "  Actual: $FORWARDED forwarding events"
    elif [ "$FORWARDED" -eq "$RECEIVED" ]; then
        echo ""
        echo "  ✅ Normal: Each received event was forwarded once"
        echo "  With 2 endpoints, this means $((FORWARDED * 2)) HTTP requests were made"
    fi
fi

echo ""
echo "=========================================="
