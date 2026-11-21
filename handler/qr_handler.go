package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"bcn-noti-service/zalo_bot"
)

// QRSession thông tin phiên quét mã QR đăng nhập Zalo
type QRSession struct {
	Code        string
	Version     string
	Client      *http.Client
	Jar         *cookiejar.Jar
	CreatedAt   time.Time
	Status      string // "WAITING_SCAN", "SCANNED", "CONFIRMED", "DECLINED", "EXPIRED", "ERROR"
	DisplayName string
	Avatar      string
	ErrorMsg    string
	CancelFunc  context.CancelFunc
}

var (
	qrSessionsMu sync.RWMutex
	qrSessions   = make(map[string]*QRSession)
)

var versionRegex = regexp.MustCompile(`https:\/\/stc-zlogin\.zdn\.vn\/main-([\d.]+)\.js`)

// SetupQRRoutes đăng ký các API phục vụ luồng QR Login tự động lấy Cookie
func SetupQRRoutes(cookieMgr *zalo_bot.CookieManager) {

	// 1. GET /api/test/bot/qr/generate: Khởi tạo phiên quét mã QR
	http.HandleFunc("/api/test/bot/qr/generate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		jar, err := cookiejar.New(nil)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Lỗi khởi tạo CookieJar: %v"}`, err), http.StatusInternalServerError)
			return
		}

		client := &http.Client{
			Jar:     jar,
			Timeout: 40 * time.Second,
		}

		// Bước 1: Load trang id.zalo.me để lấy phiên bản login version
		reqPage, err := http.NewRequest(http.MethodGet, "https://id.zalo.me/account?continue=https%3A%2F%2Fchat.zalo.me%2F", nil)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Lỗi tạo request: %v"}`, err), http.StatusInternalServerError)
			return
		}
		reqPage.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36")
		reqPage.Header.Set("Referer", "https://chat.zalo.me/")

		respPage, err := client.Do(reqPage)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Không thể kết nối máy chủ Zalo ID: %v"}`, err), http.StatusBadGateway)
			return
		}
		pageBytes, _ := io.ReadAll(respPage.Body)
		respPage.Body.Close()

		loginVersion := "2606161423"
		match := versionRegex.FindSubmatch(pageBytes)
		if len(match) > 1 {
			loginVersion = string(match[1])
		}

		// Bước 2: Gọi logininfo
		formInfo := url.Values{}
		formInfo.Set("continue", "https://zalo.me/pc")
		formInfo.Set("v", loginVersion)
		reqInfo, _ := http.NewRequest(http.MethodPost, "https://id.zalo.me/account/logininfo", strings.NewReader(formInfo.Encode()))
		reqInfo.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqInfo.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36")
		reqInfo.Header.Set("Referer", "https://id.zalo.me/account?continue=https%3A%2F%2Fzalo.me%2Fpc")
		if respInfo, err := client.Do(reqInfo); err == nil {
			_, _ = io.Copy(io.Discard, respInfo.Body)
			respInfo.Body.Close()
		}

		// Bước 3: Gọi verify-client
		formVerify := url.Values{}
		formVerify.Set("type", "device")
		formVerify.Set("continue", "https://zalo.me/pc")
		formVerify.Set("v", loginVersion)
		reqVerify, _ := http.NewRequest(http.MethodPost, "https://id.zalo.me/account/verify-client", strings.NewReader(formVerify.Encode()))
		reqVerify.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqVerify.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36")
		reqVerify.Header.Set("Referer", "https://id.zalo.me/account?continue=https%3A%2F%2Fzalo.me%2Fpc")
		if respVerify, err := client.Do(reqVerify); err == nil {
			_, _ = io.Copy(io.Discard, respVerify.Body)
			respVerify.Body.Close()
		}

		// Bước 4: Gọi generate QR
		formGen := url.Values{}
		formGen.Set("continue", "https://zalo.me/pc")
		formGen.Set("v", loginVersion)
		reqGen, _ := http.NewRequest(http.MethodPost, "https://id.zalo.me/account/authen/qr/generate", strings.NewReader(formGen.Encode()))
		reqGen.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqGen.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36")
		reqGen.Header.Set("Referer", "https://id.zalo.me/account?continue=https%3A%2F%2Fzalo.me%2Fpc")

		respGen, err := client.Do(reqGen)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Lỗi tạo mã QR từ Zalo: %v"}`, err), http.StatusBadGateway)
			return
		}
		defer respGen.Body.Close()

		var genResp struct {
			ErrorCode int `json:"error_code"`
			Data      struct {
				Code  string `json:"code"`
				Image string `json:"image"`
			} `json:"data"`
			ErrorMessage string `json:"error_message"`
		}
		if err := json.NewDecoder(respGen.Body).Decode(&genResp); err != nil || genResp.ErrorCode != 0 || genResp.Data.Code == "" {
			http.Error(w, fmt.Sprintf(`{"error":"Zalo từ chối sinh mã QR: %s"}`, genResp.ErrorMessage), http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		sess := &QRSession{
			Code:       genResp.Data.Code,
			Version:    loginVersion,
			Client:     client,
			Jar:        jar,
			CreatedAt:  time.Now(),
			Status:     "WAITING_SCAN",
			CancelFunc: cancel,
		}

		qrSessionsMu.Lock()
		qrSessions[sess.Code] = sess
		qrSessionsMu.Unlock()

		// Chạy tiến trình ngầm lắng nghe hành động quét và xác nhận trên app điện thoại
		go runQRPollingWorker(ctx, sess, cookieMgr)

		log.Printf("[QRLogin] Đã sinh mã QR thành công (Code: %s, Version: %s)", sess.Code, loginVersion)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"code":    sess.Code,
			"image":   genResp.Data.Image,
			"status":  "WAITING_SCAN",
		})
	})

	// 2. GET /api/test/bot/qr/poll: Kiểm tra tiến độ quét và xác nhận QR
	http.HandleFunc("/api/test/bot/qr/poll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, `{"error":"Thiếu tham số code"}`, http.StatusBadRequest)
			return
		}

		qrSessionsMu.RLock()
		sess, exists := qrSessions[code]
		qrSessionsMu.RUnlock()

		if !exists {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "EXPIRED",
				"error":  "Phiên đăng nhập QR đã hết hạn hoặc không tồn tại.",
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":       sess.Status,
			"display_name": sess.DisplayName,
			"avatar":       sess.Avatar,
			"error":        sess.ErrorMsg,
		})
	})

	// 3. POST /api/test/bot/qr/cancel: Hủy phiên quét QR
	http.HandleFunc("/api/test/bot/qr/cancel", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		code := r.URL.Query().Get("code")
		if code != "" {
			qrSessionsMu.Lock()
			if sess, exists := qrSessions[code]; exists {
				if sess.CancelFunc != nil {
					sess.CancelFunc()
				}
				delete(qrSessions, code)
			}
			qrSessionsMu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"cancelled": true})
	})
}

// runQRPollingWorker tiến trình ngầm xử lý waiting-scan và waiting-confirm
func runQRPollingWorker(ctx context.Context, sess *QRSession, cookieMgr *zalo_bot.CookieManager) {
	defer func() {
		// Dọn dẹp phiên sau 3 phút
		time.Sleep(3 * time.Minute)
		qrSessionsMu.Lock()
		delete(qrSessions, sess.Code)
		qrSessionsMu.Unlock()
	}()

	startTime := time.Now()

	// Giai đoạn 1: Chờ quét QR (waiting-scan)
	for {
		if time.Since(startTime) > 100*time.Second {
			sess.Status = "EXPIRED"
			log.Printf("[QRLogin] Phiên QR %s đã hết hạn (quá 100s)", sess.Code)
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}

		formScan := url.Values{}
		formScan.Set("code", sess.Code)
		formScan.Set("continue", "https://chat.zalo.me/")
		formScan.Set("v", sess.Version)

		reqScan, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://id.zalo.me/account/authen/qr/waiting-scan", strings.NewReader(formScan.Encode()))
		reqScan.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqScan.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36")
		reqScan.Header.Set("Referer", "https://id.zalo.me/account?continue=https%3A%2F%2Fchat.zalo.me%2F")

		respScan, err := sess.Client.Do(reqScan)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(1 * time.Second)
			continue
		}

		var scanResult struct {
			ErrorCode int `json:"error_code"`
			Data      struct {
				DisplayName string `json:"display_name"`
				Avatar      string `json:"avatar"`
			} `json:"data"`
		}
		_ = json.NewDecoder(respScan.Body).Decode(&scanResult)
		respScan.Body.Close()

		if scanResult.ErrorCode == 8 {
			// Đang chờ quét tiếp
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if scanResult.ErrorCode == 0 {
			// Đã quét thành công!
			sess.Status = "SCANNED"
			sess.DisplayName = scanResult.Data.DisplayName
			sess.Avatar = scanResult.Data.Avatar
			log.Printf("[QRLogin] Đã quét mã QR! Người dùng: %s", sess.DisplayName)
			break
		}

		// Mã lỗi khác
		sess.Status = "EXPIRED"
		return
	}

	// Giai đoạn 2: Chờ người dùng bấm "Xác nhận đăng nhập" trên điện thoại (waiting-confirm)
	for {
		if time.Since(startTime) > 120*time.Second {
			sess.Status = "EXPIRED"
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}

		formConfirm := url.Values{}
		formConfirm.Set("code", sess.Code)
		formConfirm.Set("gToken", "")
		formConfirm.Set("gAction", "CONFIRM_QR")
		formConfirm.Set("continue", "https://chat.zalo.me/")
		formConfirm.Set("v", sess.Version)

		reqConfirm, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://id.zalo.me/account/authen/qr/waiting-confirm", strings.NewReader(formConfirm.Encode()))
		reqConfirm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqConfirm.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36")
		reqConfirm.Header.Set("Referer", "https://id.zalo.me/account?continue=https%3A%2F%2Fchat.zalo.me%2F")

		respConfirm, err := sess.Client.Do(reqConfirm)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(1 * time.Second)
			continue
		}

		var confirmResult struct {
			ErrorCode    int    `json:"error_code"`
			ErrorMessage string `json:"error_message"`
		}
		_ = json.NewDecoder(respConfirm.Body).Decode(&confirmResult)
		respConfirm.Body.Close()

		if confirmResult.ErrorCode == 8 {
			// Đang chờ xác nhận trên điện thoại
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if confirmResult.ErrorCode == -13 {
			sess.Status = "DECLINED"
			log.Printf("[QRLogin] Người dùng từ chối xác nhận đăng nhập trên điện thoại.")
			return
		}

		if confirmResult.ErrorCode != 0 {
			sess.Status = "ERROR"
			sess.ErrorMsg = confirmResult.ErrorMessage
			log.Printf("[QRLogin] Lỗi xác nhận: %s (code %d)", confirmResult.ErrorMessage, confirmResult.ErrorCode)
			return
		}

		// Xác nhận thành công!
		break
	}

	// Giai đoạn 3: Gọi checksession & jr/userinfo để gom trọn bộ Cookies phiên mới
	reqCheck, _ := http.NewRequest(http.MethodGet, "https://id.zalo.me/account/checksession?continue=https%3A%2F%2Fchat.zalo.me%2Findex.html", nil)
	reqCheck.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36")
	reqCheck.Header.Set("Referer", "https://id.zalo.me/account?continue=https%3A%2F%2Fchat.zalo.me%2F")
	if respCheck, err := sess.Client.Do(reqCheck); err == nil {
		_, _ = io.Copy(io.Discard, respCheck.Body)
		respCheck.Body.Close()
	}

	reqUser, _ := http.NewRequest(http.MethodGet, "https://jr.chat.zalo.me/jr/userinfo", nil)
	reqUser.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36")
	reqUser.Header.Set("Referer", "https://chat.zalo.me/")
	if respUser, err := sess.Client.Do(reqUser); err == nil {
		_, _ = io.Copy(io.Discard, respUser.Body)
		respUser.Body.Close()
	}

	// Trích xuất toàn bộ cookie từ Jar
	cookieMap := make(map[string]string)
	urlsToInspect := []string{
		"https://chat.zalo.me",
		"https://id.zalo.me",
		"https://zalo.me",
		"https://jr.chat.zalo.me",
	}

	for _, targetURL := range urlsToInspect {
		u, err := url.Parse(targetURL)
		if err == nil && sess.Jar != nil {
			for _, c := range sess.Jar.Cookies(u) {
				cookieMap[c.Name] = c.Value
			}
		}
	}

	var cookiePairs []string
	for k, v := range cookieMap {
		cookiePairs = append(cookiePairs, fmt.Sprintf("%s=%s", k, v))
	}
	fullCookieString := strings.Join(cookiePairs, "; ")

	if cookieMgr != nil && fullCookieString != "" {
		if err := cookieMgr.SaveSession(fullCookieString, "", ""); err != nil {
			log.Printf("[QRLogin] Lỗi lưu session mới: %v", err)
			sess.Status = "ERROR"
			sess.ErrorMsg = "Không thể ghi file cookie: " + err.Error()
			return
		}
		log.Printf("[QRLogin] ĐÃ TỰ ĐỘNG LẤY VÀ LƯU COOKIE THÀNH CÔNG! Số cookie: %d (%s)", len(cookieMap), sess.DisplayName)
	}

	sess.Status = "CONFIRMED"
}
