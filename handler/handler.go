package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"bcn-noti-service/config"
	"bcn-noti-service/provider"
	"bcn-noti-service/service"
	"bcn-noti-service/zalo_bot"
)

var pkceStore sync.Map

// taoPKCE tạo mã code_verifier và code_challenge theo chuẩn Zalo OAuth 2.0 PKCE
func taoPKCE() (verifier string, challenge string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return verifier, challenge
}

// ZaloConfig lưu thông số kết nối Zalo for Developers
type ZaloConfig struct {
	AppID         string
	AppSecret     string
	RedirectURI   string
	MentorUIDs    []string
	ZaloProvider  *provider.ZaloProvider
	CookieManager *zalo_bot.CookieManager
}

// ZaloUserProfile cấu trúc phản hồi từ Zalo Graph API khi lấy UID thật
type ZaloUserProfile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Picture struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	} `json:"picture"`
	Error int    `json:"error"`
	Msg   string `json:"message"`
}

// ZaloTokenResponse cấu trúc nhận token từ Zalo OAuth
type ZaloTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   string `json:"expires_in"`
	Error       int    `json:"error"`
}

// SetupZaloAuthRoutes thiết lập các đường dẫn đăng nhập Zalo thật
func SetupZaloAuthRoutes(cfg ZaloConfig) {

	// 1. Endpoint bắt đầu đăng nhập: Chuyển hướng người dùng sang trang chính thức của Zalo
	http.HandleFunc("/auth/zalo/login", func(w http.ResponseWriter, r *http.Request) {
		_ = config.LoadEnv(".env") // Tự động đọc lại file .env nếu người dùng vừa lưu
		appID := os.Getenv("ZALO_APP_ID")
		if appID == "" {
			appID = cfg.AppID
		}

		redirectURI := os.Getenv("ZALO_REDIRECT_URI")
		if redirectURI == "" {
			redirectURI = cfg.RedirectURI
		}
		if redirectURI == "" {
			redirectURI = "http://localhost:8080/auth/zalo/callback"
		}

		// Nếu chưa cấu hình Zalo App ID từ developers.zalo.me
		if appID == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `
				<div style="font-family: sans-serif; padding: 40px; text-align: center;">
					<h2 style="color: #c2410c;">Chưa cấu hình ZALO_APP_ID trong file .env!</h2>
					<p>Để đăng nhập Zalo thật (không giả lập), bạn cần:</p>
					<ol style="display: inline-block; text-align: left; line-height: 1.8;">
						<li>Truy cập <a href="https://developers.zalo.me" target="_blank">developers.zalo.me</a> tạo 1 Ứng dụng Zalo.</li>
						<li>Lấy <b>App ID</b> và <b>App Secret</b> điền vào file <code>.env</code>.</li>
						<li>Thêm đường dẫn chuyển hướng (Callback): <code>http://localhost:8080/auth/zalo/callback</code></li>
					</ol>
					<br><br>
					<a href="/" style="padding: 10px 20px; background: #0068ff; color: white; text-decoration: none; border-radius: 8px;">Quay lại trang chủ</a>
				</div>
			`)
			return
		}

		// Chuyển hướng thẳng sang cổng cấp quyền Zalo chính thức kèm chuẩn bảo mật PKCE
		state := fmt.Sprintf("bcn_state_%d", time.Now().UnixNano())
		verifier, challenge := taoPKCE()
		pkceStore.Store(state, verifier)

		zaloAuthURL := fmt.Sprintf(
			"https://oauth.zaloapp.com/v4/permission?app_id=%s&redirect_uri=%s&code_challenge=%s&state=%s",
			url.QueryEscape(appID),
			url.QueryEscape(redirectURI),
			url.QueryEscape(challenge),
			url.QueryEscape(state),
		)

		http.Redirect(w, r, zaloAuthURL, http.StatusTemporaryRedirect)
	})

	// 2. Endpoint Callback: Zalo xác thực thành công và trả Authorization Code về đây
	http.HandleFunc("/auth/zalo/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Không nhận được mã xác thực từ Zalo!", http.StatusBadRequest)
			return
		}

		state := r.URL.Query().Get("state")
		codeVerifier := ""
		if val, ok := pkceStore.Load(state); ok {
			codeVerifier = val.(string)
			pkceStore.Delete(state)
		}

		appID := cfg.AppID
		if appID == "" {
			appID = os.Getenv("ZALO_APP_ID")
		}
		appSecret := cfg.AppSecret
		if appSecret == "" {
			appSecret = os.Getenv("ZALO_APP_SECRET")
		}

		// Đổi Code lấy Access Token từ máy chủ Zalo
		tokenURL := "https://oauth.zaloapp.com/v4/access_token"
		formData := url.Values{}
		formData.Set("app_id", appID)
		formData.Set("code", code)
		formData.Set("grant_type", "authorization_code")
		if codeVerifier != "" {
			formData.Set("code_verifier", codeVerifier)
		}

		req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(formData.Encode()))
		if err != nil {
			http.Error(w, "Lỗi tạo yêu cầu đổi token", http.StatusInternalServerError)
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("secret_key", appSecret)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, "Không kết nối được tới máy chủ Zalo Token", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		var tokenResp ZaloTokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil || tokenResp.AccessToken == "" {
			http.Error(w, "Lỗi giải mã Access Token từ Zalo", http.StatusBadRequest)
			return
		}

		// Dùng Access Token gọi Zalo Graph API để lấy UID THẬT và Tên thật
		profileURL := "https://graph.zalo.me/v2.0/me?fields=id,name,picture"
		profReq, _ := http.NewRequest(http.MethodGet, profileURL, nil)
		profReq.Header.Set("access_token", tokenResp.AccessToken)

		profResp, err := http.DefaultClient.Do(profReq)
		if err != nil {
			http.Error(w, "Lỗi gọi Zalo Graph API lấy UID thật", http.StatusInternalServerError)
			return
		}
		defer profResp.Body.Close()

		bodyBytes, _ := io.ReadAll(profResp.Body)
		var userProfile ZaloUserProfile
		if err := json.Unmarshal(bodyBytes, &userProfile); err != nil || userProfile.ID == "" {
			http.Error(w, "Zalo không trả về ID người dùng", http.StatusBadRequest)
			return
		}

		log.Printf("[Zalo-Auth] Đăng nhập thật thành công! Tên: %s | UID Thật: %s", userProfile.Name, userProfile.ID)

		// Đối soát UID thật với file members_cache.json hoặc MENTOR_ZALO_UIDS
		role := service.RoleUser
		fullName := userProfile.Name

		if cfg.ZaloProvider != nil {
			cachedRole, cachedName := cfg.ZaloProvider.CheckRole(userProfile.ID)
			if cachedRole == "mentor" {
				role = service.RoleMentor
			} else if cachedRole == "user" {
				role = service.RoleUser
			} else {
				isMentor := false
				for _, mentorUID := range cfg.MentorUIDs {
					if userProfile.ID == mentorUID {
						isMentor = true
						break
					}
				}
				if isMentor {
					role = service.RoleMentor
				} else {
					role = service.RoleUser
				}
				cfg.ZaloProvider.UpsertMember(userProfile.ID, userProfile.Name, isMentor)
			}
			if cachedName != "" {
				fullName = cachedName
			}
		} else {
			nguoiDung := service.IdentifyUser(userProfile.ID, userProfile.Name, cfg.MentorUIDs)
			role = nguoiDung.Role
		}

		redirectClientURL := fmt.Sprintf("/?zalo_uid=%s&name=%s&role=%s",
			url.QueryEscape(userProfile.ID),
			url.QueryEscape(fullName),
			url.QueryEscape(role),
		)
		http.Redirect(w, r, redirectClientURL, http.StatusTemporaryRedirect)
	})

	http.HandleFunc("/api/members", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if cfg.ZaloProvider != nil {
			cache := cfg.ZaloProvider.GetCache()
			if cache != nil {
				_ = json.NewEncoder(w).Encode(cache)
				return
			}
		}

		data, err := os.ReadFile("members_cache.json")
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"mentors":[],"members":[]}`))
			return
		}
		_, _ = w.Write(data)
	})

	http.HandleFunc("/api/members/sync", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if cfg.ZaloProvider == nil {
			http.Error(w, `{"error":"Chưa cấu hình ZaloProvider"}`, http.StatusBadRequest)
			return
		}
		cache, err := cfg.ZaloProvider.SyncMemberCache("")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":   "Không thể đồng bộ từ nhóm Zalo lúc này",
				"details": err.Error(),
			})
			return
		}
		_ = json.NewEncoder(w).Encode(cache)
	})
}

// StartServer khởi chạy toàn bộ máy chủ Web và cổng Zalo OAuth thật
func StartServer(duongDanThuMucWeb string, congServer string, cfg ZaloConfig) error {
	if congServer == "" {
		congServer = ":8080"
	}

	SetupZaloAuthRoutes(cfg)
	SetupPostAndBotRoutes(cfg, cfg.CookieManager)
	SetupTestRoutes(cfg, cfg.CookieManager)
	SetupQRRoutes(cfg.CookieManager)

	// Phục vụ giao diện web
	fileServer := http.FileServer(http.Dir(duongDanThuMucWeb))
	http.Handle("/", fileServer)

	log.Printf("Web server running at http://localhost%s", congServer)
	return http.ListenAndServe(congServer, nil)
}
