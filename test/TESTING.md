# Hướng dẫn Test Telephony Forwarder

## Cách test ứng dụng có forward được tín hiệu hay không

### Bước 1: Đảm bảo các service đang chạy

1. **NATS Server** phải đang chạy:
```bash
# Kiểm tra NATS
curl http://localhost:8222/healthz 2>/dev/null || echo "NATS chưa chạy"
```

2. **Telephony Forwarder** phải đang chạy:
```bash
# Kiểm tra health
curl http://localhost:8080/health
```

### Bước 2: Khởi động Mock Backend Server

Mock server sẽ nhận các events được forward từ telephony-forwarder:

```bash
# Chạy mock server trên port 9000
python3 test_mock_server.py 9000
```

Hoặc nếu muốn chạy trên port khác:
```bash
python3 test_mock_server.py 9001
```

**Lưu ý:** Giữ terminal này mở để xem events được nhận.

### Bước 3: Cấu hình routes trong config.yaml

Đảm bảo `config.yaml` có route trỏ đến mock server:

```yaml
routes:
  - domain: "vietanh.cloudpro.vn"
    endpoints:
      - "http://localhost:9000/webhook"
```

Hoặc sử dụng `test_config.yaml` đã được cấu hình sẵn:
```bash
./cmd/app -config test_config.yaml -log-level info
```

### Bước 4: Gửi test event

**Cách 1: Sử dụng script test**
```bash
./test_send_event.sh
```

**Cách 2: Sử dụng curl trực tiếp**
```bash
curl -X POST http://localhost:8080/events \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "test-123",
    "domain": "vietanh.cloudpro.vn",
    "direction": "inbound",
    "from_number": "0914315989",
    "hotline": "02743857008",
    "state": "missed",
    "status": "busy-line",
    "time_started": "2026-01-04 16:18:12",
    "time_ended": "2026-01-04 16:19:14"
  }'
```

**Cách 3: Sử dụng file JSON**
```bash
# Tạo file test_event.json
cat > test_event.json << 'EOF'
{
  "call_id": "test-$(date +%s)",
  "domain": "vietanh.cloudpro.vn",
  "direction": "inbound",
  "from_number": "0914315989",
  "hotline": "02743857008",
  "state": "missed",
  "status": "busy-line",
  "time_started": "2026-01-04 16:18:12",
  "time_ended": "2026-01-04 16:19:14"
}
EOF

curl -X POST http://localhost:8080/events \
  -H "Content-Type: application/json" \
  -d @test_event.json
```

### Bước 5: Kiểm tra kết quả

#### 1. Kiểm tra response từ event-hub
- **HTTP 200 OK**: Event đã được nhận và publish vào NATS ✅
- **HTTP 400 Bad Request**: Payload không hợp lệ hoặc thiếu `domain` ❌
- **HTTP 500 Internal Server Error**: Lỗi khi publish vào NATS ❌

#### 2. Kiểm tra Mock Backend Server
Nếu forward thành công, bạn sẽ thấy trong terminal của mock server:
```
============================================================
✅ EVENT RECEIVED!
============================================================
URL: /webhook
Call ID: test-123
Domain: vietanh.cloudpro.vn
...
============================================================
```

#### 3. Kiểm tra logs của telephony-forwarder
Xem logs để kiểm tra:
- Event có được publish vào NATS không
- Consumer có nhận được message không
- Forward có thành công không

```bash
# Xem logs real-time (nếu chạy trong foreground)
./cmd/app -config config.yaml -log-level debug
```

### Test Cases

#### Test 1: Forward thành công
```bash
# Gửi event với domain hợp lệ
curl -X POST http://localhost:8080/events \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "test-success",
    "domain": "vietanh.cloudpro.vn",
    "direction": "inbound",
    "state": "answered",
    "status": "completed"
  }'
```

**Kỳ vọng:**
- HTTP 202 từ event-hub
- Mock server nhận được event
- Logs hiển thị "Event forwarded successfully"

#### Test 2: Domain không tồn tại
```bash
curl -X POST http://localhost:8080/events \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "test-no-route",
    "domain": "unknown.domain.com",
    "direction": "inbound"
  }'
```

**Kỳ vọng:**
- HTTP 202 từ event-hub (event vẫn được publish)
- Logs hiển thị "No routes found for domain"
- Message sẽ bị NAK và redeliver

#### Test 3: Thiếu domain
```bash
curl -X POST http://localhost:8080/events \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "test-no-domain",
    "direction": "inbound"
  }'
```

**Kỳ vọng:**
- HTTP 400 Bad Request
- Response: "domain is required"

#### Test 4: Backend timeout
```bash
# Tạo một mock server trả về chậm hoặc không phản hồi
# Sau đó gửi event
```

**Kỳ vọng:**
- Event được forward nhưng timeout sau 3 giây
- Message không được ACK
- JetStream sẽ redeliver sau 10 giây (ack_wait_seconds)

#### Test 5: Multiple endpoints
Cấu hình nhiều endpoints cho cùng một domain:
```yaml
routes:
  - domain: "vietanh.cloudpro.vn"
    endpoints:
      - "http://localhost:9000/webhook"
      - "http://localhost:9001/webhook"
```

**Kỳ vọng:**
- Event được forward đến TẤT CẢ endpoints đồng thời
- Chỉ ACK khi TẤT CẢ endpoints thành công

### Troubleshooting

#### Event không được forward
1. Kiểm tra NATS connection:
   ```bash
   curl http://localhost:8080/health
   ```

2. Kiểm tra domain trong event có khớp với config:
   ```bash
   # Xem config
   cat config.yaml | grep -A 3 "domain:"
   ```

3. Kiểm tra logs của telephony-forwarder để xem lỗi

#### Mock server không nhận được events
1. Kiểm tra mock server có đang chạy:
   ```bash
   curl http://localhost:9000/
   ```

2. Kiểm tra URL trong config.yaml có đúng không

3. Kiểm tra firewall/network có block không

#### Event bị redeliver nhiều lần
- Có thể backend endpoint trả về lỗi
- Kiểm tra logs để xem lỗi cụ thể
- Message sẽ được redeliver tối đa 3 lần (max_deliveries)

### Tips

1. **Sử dụng log-level debug** để xem chi tiết:
   ```bash
   ./cmd/app -config config.yaml -log-level debug
   ```

2. **Test với nhiều events** để kiểm tra concurrent processing:
   ```bash
   for i in {1..10}; do
     ./test_send_event.sh
     sleep 0.5
   done
   ```

3. **Monitor NATS** để xem messages trong stream:
   ```bash
   # Cài đặt nats CLI nếu chưa có
   nats stream info call-signals
   nats consumer info call-signals event-hub-consumer
   ```

