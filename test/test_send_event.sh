#!/bin/bash
# Script để test gửi event đến telephony-forwarder

# Màu sắc cho output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${BLUE}🧪 Test Telephony Forwarder${NC}"
echo "================================"
echo ""

# URL của event-hub service
EVENT_HUB_URL="${EVENT_HUB_URL:-http://localhost:8080}"

# Sample event data
EVENT_DATA='{
  "actual_hotline": "",
  "billsec": "62",
  "call_id": "test-'$(date +%s)'",
  "crm_contact_id": "",
  "direction": "inbound",
  "domain": "vietanh.cloudpro.vn",
  "duration": "63",
  "from_number": "0914315989",
  "hotline": "02743857008",
  "network": "vina",
  "provider": "",
  "receive_dest": "2006",
  "sip_call_id": "test-sip-call-id",
  "sip_hangup_disposition": "recv_bye",
  "state": "missed",
  "status": "busy-line",
  "time_ended": "'$(date +"%Y-%m-%d %H:%M:%S")'",
  "time_started": "'$(date -d "1 minute ago" +"%Y-%m-%d %H:%M:%S")'",
  "to_number": ""
}'

echo -e "${YELLOW}📤 Gửi event đến: ${EVENT_HUB_URL}/events${NC}"
echo ""

# Gửi POST request
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST \
  -H "Content-Type: application/json" \
  -d "$EVENT_DATA" \
  "$EVENT_HUB_URL/events")

# Tách response body và status code
HTTP_BODY=$(echo "$RESPONSE" | head -n -1)
HTTP_CODE=$(echo "$RESPONSE" | tail -n 1)

echo -e "${BLUE}Response Status: ${HTTP_CODE}${NC}"
echo -e "${BLUE}Response Body:${NC}"
echo "$HTTP_BODY" | jq . 2>/dev/null || echo "$HTTP_BODY"
echo ""

if [ "$HTTP_CODE" = "202" ]; then
    echo -e "${GREEN}✅ Event đã được chấp nhận và gửi vào NATS JetStream${NC}"
    echo -e "${YELLOW}⏳ Đợi vài giây để consumer forward event đến backend...${NC}"
    echo ""
    echo "Kiểm tra:"
    echo "  1. Logs của telephony-forwarder (xem có forward thành công không)"
    echo "  2. Mock backend server (xem có nhận được event không)"
else
    echo -e "${YELLOW}⚠️  Event không được chấp nhận (HTTP $HTTP_CODE)${NC}"
    echo "Kiểm tra:"
    echo "  1. Ứng dụng có đang chạy không: curl $EVENT_HUB_URL/health"
    echo "  2. NATS server có đang chạy không"
    echo "  3. Domain trong event có khớp với config.yaml không"
fi

