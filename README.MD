<div align="center">

#  BCN NOTIFICATION HUB SERVICE

### *Enterprise-Grade Multi-Channel Notification Engine with Role-Based Access Control*

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://golang.org)
[![Architecture](https://img.shields.io/badge/Architecture-Clean%20%2F%20Hexagonal-555555?style=for-the-badge&logo=codereview&logoColor=white)](#-kiến-trúc-tổng-thể)
[![RBAC Engine](https://img.shields.io/badge/Auth-Zalo%20UID%20Whitelist-blue?style=for-the-badge&logo=zalo&logoColor=white)](#-cơ-chế-phân-quyền-rbac-engine)
[![License](https://img.shields.io/badge/License-MIT-green?style=for-the-badge)](LICENSE)

*Hệ thống trung tâm điều phối thông báo đa nền tảng (Zalo Group, Discord Webhook) bảo đảm toàn vẹn dữ liệu, kiểm soát duyệt bài nghiêm ngặt qua danh sách trắng định danh.*

---

</div>

##  Tổng Quan Hệ Thống

**BCN Notification Service** được xây dựng nhằm giải quyết bài toán phát tán thông báo tự động, đồng bộ và có kiểm duyệt cho Ban Chủ Nhiệm (BCN):
* **Bảo mật tuyệt đối (Zero-Password):** Nhận diện trực tiếp qua `ZaloUID` và đối soát tức thì với danh sách Mentor Whitelist cấu hình độc lập.
* **Quy trình kiểm duyệt 2 lớp:** Mọi bài đăng từ thành viên đều bắt buộc đi qua hàng đợi phê duyệt trước khi được phát sóng ra các kênh ngoại vi.
* **Hỗ trợ Đa kênh & Hẹn giờ linh hoạt:** Lưu trữ đa kênh cùng lúc (`TargetChannels []string`) và cơ chế đặt lịch thông minh (`PublishAt *time.Time`).

---

## Kiến Trúc Tổng Thể

Hệ thống áp dụng mô hình **Clean Architecture / Interface Provider Pattern**:
