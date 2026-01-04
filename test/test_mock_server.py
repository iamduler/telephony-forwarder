#!/usr/bin/env python3
"""
Mock backend server để test forwarding
Chạy server này để nhận forwarded events từ telephony-forwarder
"""
from http.server import HTTPServer, BaseHTTPRequestHandler
import json
from datetime import datetime

class MockBackendHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        """Override để log với timestamp"""
        timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        print(f"[{timestamp}] {format % args}")
    
    def do_POST(self):
        """Handle POST requests"""
        content_length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(content_length)
        
        try:
            event = json.loads(body.decode('utf-8'))
            
            # Log received event
            print("\n" + "="*60)
            print("✅ EVENT RECEIVED!")
            print("="*60)
            print(f"URL: {self.path}")
            print(f"Headers: {dict(self.headers)}")
            print(f"Call ID: {event.get('call_id', 'N/A')}")
            print(f"Domain: {event.get('domain', 'N/A')}")
            print(f"Direction: {event.get('direction', 'N/A')}")
            print(f"State: {event.get('state', 'N/A')}")
            print(f"Status: {event.get('status', 'N/A')}")
            print(f"From: {event.get('from_number', 'N/A')}")
            print(f"To: {event.get('to_number', 'N/A')}")
            print(f"Hotline: {event.get('hotline', 'N/A')}")
            print("Full Event:")
            print(json.dumps(event, indent=2, ensure_ascii=False))
            print("="*60 + "\n")
            
            # Return success
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps({"status": "ok", "received": True}).encode())
            
        except Exception as e:
            print(f"❌ Error processing request: {e}")
            self.send_response(500)
            self.end_headers()
            self.wfile.write(json.dumps({"error": str(e)}).encode())
    
    def do_GET(self):
        """Handle GET requests (health check)"""
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps({"status": "ok"}).encode())

def run_server(port=9000):
    server_address = ('', port)
    httpd = HTTPServer(server_address, MockBackendHandler)
    print(f"🚀 Mock Backend Server đang chạy trên port {port}")
    print(f"📡 Đang chờ nhận forwarded events...")
    print(f"   URL: http://localhost:{port}/webhook")
    print(f"\n⚠️  Đảm bảo config.yaml trỏ đến: http://localhost:{port}/webhook")
    print("="*60 + "\n")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\n\n🛑 Đang dừng server...")
        httpd.shutdown()

if __name__ == '__main__':
    import sys
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 9000
    run_server(port)

