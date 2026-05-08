# DOCUMENT

## 1. Sơ đồ hệ thống



![alt text](<diagram.png>)

**Giải thích luồng dữ liệu (Data Flow):**
1. **Request:** Người dùng gửi request (GET/POST) tới cổng 80 của Nginx (Load Balancer).
2. **Load Balancing:** Nginx sử dụng thuật toán Round Robin để phân phối đều request cho `Node_1` và `Node_2`.
3. **Read/Write Splitting:**
   - Nếu là request `POST` (Tạo sản phẩm mới): API Node sẽ mở kết nối và ghi thẳng dữ liệu vào **Database Master**.
   - Nếu là request `GET` (Lấy danh sách): API Node sẽ mở kết nối và đọc dữ liệu từ **Database Slave**.
4. **Replication:** Bất cứ khi nào có dữ liệu mới ghi vào Master, Master sẽ tự động đồng bộ (sync) dữ liệu đó sang Slave.

---

## 2. Cấu hình

### 2.1. Nginx Load Balancer (`nginx.conf`)
Nginx đóng vai trò là cổng giao tiếp duy nhất (Reverse Proxy) và cân bằng tải.
```nginx
upstream api_servers {
    # Thuật toán mặc định là Round Robin (chia đều 1-1)
    server node_1:8080;
    server node_2:8080;
}
server {
    listen 80;
    location / {
        proxy_pass http://api_servers; # Trỏ toàn bộ traffic vào upstream
    }
}
```
**Giải thích:** Cấu hình `upstream` khai báo 2 instances của Golang API đang chạy ở port 8080 trong mạng nội bộ của Docker. Mọi request đến cổng 80 của Nginx sẽ được đẩy lần lượt luân phiên cho `node_1` và `node_2`.

### 2.2. Database Master-Slave Replication (`docker-compose.yml`)
Sử dụng image Bitnami PostgreSQL với các biến môi trường được thiết kế sẵn cho việc Replication.
```yaml
# Cấu hình trên Master Node
- POSTGRESQL_REPLICATION_MODE=master
- POSTGRESQL_REPLICATION_USER=repl_user

# Cấu hình trên Slave Node
- POSTGRESQL_REPLICATION_MODE=slave
- POSTGRESQL_MASTER_HOST=postgresql-master
```
**Giải thích:** Master node được set quyền `master` và tạo một user dùng để replication. Slave node được set quyền `slave` và được chỉ định trỏ tới `HOST` của Master để tự động lắng nghe và copy dữ liệu liên tục.

### 2.3. API Read/Write Splitting Logic (`main.go`)
Code Golang khởi tạo 2 Connection Pools riêng biệt và định tuyến request dựa trên HTTP Method.
```go
// 1. Tạo 2 connection riêng biệt
masterDB, _ = sql.Open("postgres", masterConnStr) // Trỏ tới Master Host
slaveDB, _  = sql.Open("postgres", slaveConnStr)  // Trỏ tới Slave Host

// 2. Định tuyến (Routing)
if r.Method == http.MethodPost {
    // GHI dữ liệu -> Dùng masterDB
    err := masterDB.QueryRow("INSERT INTO products (name, price) VALUES ($1, $2)", p.Name, p.Price)
} else if r.Method == http.MethodGet {
    // ĐỌC dữ liệu -> Dùng slaveDB
    rows, err := slaveDB.Query("SELECT id, name, price FROM products")
}
```
**Giải thích:** Việc tách bạch hoàn toàn `masterDB` và `slaveDB` ở tầng ứng dụng giúp giảm tải cho Master. Các tác vụ đọc chiếm đa số được đẩy hết sang Slave.

---

## 3. Hướng dẫn cài đặt và chạy thử

Tài liệu này hướng dẫn cách để bất kỳ ai có thể triển khai lại hệ thống này trên máy của họ.

### Yêu cầu hệ thống (Prerequisites)
- Đã cài đặt **Docker** và **Docker Compose**.
- Đã cài đặt phần mềm test API: **Postman** hoặc sử dụng **cURL** trên Terminal.

### Bước 1: Khởi động hệ thống
Mở Terminal tại thư mục gốc của project (nơi chứa file `docker-compose.yml`) và chạy lệnh:
```bash
docker-compose up -d --build
```
Hệ thống sẽ mất khoảng 1-2 phút để tải Image Database và Build Golang API. Hãy dùng lệnh `docker ps` để kiểm tra, đảm bảo có 5 containers đang chạy: `postgres_master`, `postgres_slave`, `golang_node_1`, `golang_node_2`, và `nginx_load_balancer`.

### Bước 2: Test tính năng Ghi (POST) và Database Replication
Mở terminal mới và chạy lệnh cURL sau để tạo một sản phẩm mới (Dữ liệu sẽ được ghi vào Master):
```bash
curl -X POST http://localhost:80/products \
-H "Content-Type: application/json" \
-d '{"name": "Laptop Dell XPS", "price": 1500.50}'
```
**Kết quả mong đợi:** Dữ liệu trả về thông báo thành công và kèm theo `"processed_by": "Node_1"` (hoặc Node_2).

### Bước 3: Test Load Balancing và Read Replica (GET)
Chạy lệnh GET sau khoảng 3 đến 4 lần liên tiếp (Dữ liệu này được đọc từ Slave):
```bash
curl http://localhost:80/products
```
**Kết quả mong đợi:** 
1. Danh sách sản phẩm vừa tạo ở Bước 2=> **Replication** hoạt động (Ghi vào Master, Đọc ở Slave vẫn có data).
2. Thuộc tính `"processed_by"` sẽ nhảy luân phiên: `Node_1` -> `Node_2` -> `Node_1` -> `Node_2`. Điều này chứng minh **Nginx Load Balancer** hoạt động hoàn hảo.

### Bước 4: Test tính chịu lỗi (Fault Tolerance / Chaos Test)
Tắt đột ngột 1 API Node để xem hệ thống có sập không.
```bash
# Tắt thủ công Node 1
docker stop golang_node_1
```
Tiếp tục gọi lại lệnh cURL GET ở Bước 3 vài lần nữa:
```bash
curl http://localhost:80/products
```
**Kết quả:** Hệ thống vẫn trả về danh sách sản phẩm bình thường không bị lỗi, nhưng lúc này toàn bộ request đều báo `"processed_by": "Node_2"`. Nginx đã tự động phát hiện Node 1 chết và điều phối toàn bộ traffic sang Node 2.

---