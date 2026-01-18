# Hướng dẫn Test với Postman

## Cách import Collection vào Postman

### Bước 1: Import Collection
1. Mở Postman
2. Click **Import** (góc trên bên trái)
3. Chọn tab **File** hoặc **Link**
4. Chọn file `Telephony_Forwarder.postman_collection.json` hoặc paste đường dẫn
5. Click **Import**

### Bước 2: Cấu hình Environment (Tùy chọn)

Tạo environment để dễ thay đổi URL:

1. Click **Environments** (bên trái)
2. Click **+** để tạo environment mới
3. Đặt tên: `Telephony Forwarder Local`
4. Thêm biến:
   - `base_url`: `http://localhost:8080`
5. Save và chọn environment này

### Bước 3: Chạy test

Collection đã có sẵn các requests:

#### 1. **Health Check**
- **Method**: GET
- **URL**: `http://localhost:8080/health`
- **Mục đích**: Kiểm tra service có đang chạy không
- **Kỳ vọng**: HTTP 200 với `{"status":"healthy"}`

#### 2. **Send Event - Success** ⭐
- **Method**: POST
- **URL**: `http://localhost:8080/events`
- **Body**: JSON với đầy đủ thông tin event
- **Domain**: `vietanh.cloudpro.vn` (phải khớp với config.yaml)
- **Mục đích**: Test forward thành công
- **Kỳ vọng**: 
  - HTTP 200 OK
  - Response: `{"status":"accepted"}`
  - Mock backend server nhận được event

#### 3. **Send Event - Missing Domain**
- **Method**: POST
- **Body**: JSON không có field `domain`
- **Mục đích**: Test validation
- **Kỳ vọng**: HTTP 400 Bad Request với message "domain is required"

#### 4. **Send Event - Unknown Domain**
- **Method**: POST
- **Domain**: `unknown.domain.com` (không có trong config)
- **Mục đích**: Test khi domain không có route
- **Kỳ vọng**: 
  - HTTP 200 (event vẫn được publish)
  - Nhưng không có endpoint nào nhận được (check logs)

#### 5. **Send Event - Answered Call**
- **Method**: POST
- **State**: `answered`
- **Status**: `completed`
- **Mục đích**: Test với event type khác

#### 6. **Send Event - Outbound Call**
- **Method**: POST
- **Direction**: `outbound`
- **Mục đích**: Test với direction khác

## Cách sử dụng

### Test cơ bản:

1. **Khởi động Mock Backend Server** (terminal):
   ```bash
   python3 test_mock_server.py 9000
   ```

2. **Đảm bảo telephony-forwarder đang chạy**:
   ```bash
   ./cmd/app -config config.yaml -log-level info
   ```

3. **Trong Postman**:
   - Chọn request **"Send Event - Success"**
   - Click **Send**
   - Kiểm tra response (phải là HTTP 200)
   - Kiểm tra Mock Backend Server terminal (phải thấy event được nhận)

### Test với dữ liệu tùy chỉnh:

1. Chọn request **"Send Event - Success"**
2. Vào tab **Body**
3. Sửa JSON theo nhu cầu:
   ```json
   {
     "call_id": "my-custom-call-id-123",
     "domain": "vietanh.cloudpro.vn",
     "direction": "inbound",
     "from_number": "0912345678",
     "hotline": "02743857008",
     "state": "answered",
     "status": "completed"
   }
   ```
4. Click **Send**

### Sử dụng Variables:

Collection có sẵn variable `{{$timestamp}}` để tự động tạo unique call_id:
- `{{$timestamp}}` - Unix timestamp hiện tại

Bạn có thể thêm variables khác:
- `{{base_url}}` - Nếu đã tạo environment

## Cấu trúc Event JSON

### Required Fields:
- `domain` (string) - **BẮT BUỘC** - Dùng để routing đến backend endpoints

### Optional Fields (nhưng nên có):
- `call_id` (string) - Unique identifier cho cuộc gọi
- `direction` (string) - `inbound` hoặc `outbound`
- `from_number` (string) - Số gọi đến
- `to_number` (string) - Số được gọi
- `hotline` (string) - Số hotline
- `state` (string) - Trạng thái: `missed`, `answered`, `busy`, etc.
- `status` (string) - Status: `completed`, `busy-line`, `no-answer`, etc.
- `time_started` (string) - Thời gian bắt đầu: `"2026-01-04 16:18:12"`
- `time_ended` (string) - Thời gian kết thúc: `"2026-01-04 16:19:14"`
- `duration` (string) - Tổng thời gian (giây)
- `billsec` (string) - Thời gian tính cước (giây)

### Ví dụ Event đầy đủ:

```json
{
  "actual_hotline": "",
  "billsec": "62",
  "call_id": "d1570d38-edc3-4751-a32d-63a30e95c57a",
  "crm_contact_id": "",
  "direction": "inbound",
  "domain": "vietanh.cloudpro.vn",
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

## Kiểm tra kết quả

### 1. Response từ Postman:
- **HTTP 200 OK**: ✅ Event đã được nhận và publish vào NATS
- **HTTP 400 Bad Request**: ❌ Payload không hợp lệ
- **HTTP 500 Internal Server Error**: ❌ Lỗi server (check logs)

### 2. Mock Backend Server:
Nếu forward thành công, bạn sẽ thấy trong terminal:
```
============================================================
✅ EVENT RECEIVED!
============================================================
URL: /webhook
Call ID: ...
Domain: vietanh.cloudpro.vn
...
============================================================
```

### 3. Logs của telephony-forwarder:
Xem logs để kiểm tra:
- `Event received and published` - Event đã được publish
- `Event forwarded successfully` - Forward thành công
- `Failed to forward event` - Forward thất bại (check lỗi)

## Troubleshooting

### Postman không kết nối được
- Kiểm tra service có đang chạy: `curl http://localhost:8080/health`
- Kiểm tra port 8080 có bị block không

### Event không được forward
- Kiểm tra `domain` trong event có khớp với `config.yaml` không
- Kiểm tra mock backend server có đang chạy không
- Kiểm tra URL trong `config.yaml` có đúng không

### Response 400 Bad Request
- Kiểm tra JSON format có đúng không
- Đảm bảo có field `domain`
- Kiểm tra Content-Type header: `application/json`

## Tips

1. **Sử dụng Collection Runner** để chạy nhiều requests:
   - Click **Run** trên collection
   - Chọn requests muốn chạy
   - Click **Run Telephony Forwarder...**

2. **Tạo Pre-request Script** để tự động tạo unique call_id:
   ```javascript
   pm.variables.set("call_id", Date.now() + "-" + Math.random().toString(36).substr(2, 9));
   ```

3. **Tạo Tests** để tự động verify response:
   ```javascript
   pm.test("Status code is 200", function () {
       pm.response.to.have.status(200);
   });
   
   pm.test("Response has status accepted", function () {
       var jsonData = pm.response.json();
       pm.expect(jsonData.status).to.eql("accepted");
   });
   ```

4. **Export Collection** sau khi customize để chia sẻ với team

