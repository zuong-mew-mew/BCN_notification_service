package zalo_bot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type Session struct {
	Cookie    string    `json:"cookie"`
	SecretKey string    `json:"khoa_bi_mat"`
	IMEI      string    `json:"ma_thiet_bi_imei"`
	UserAgent string    `json:"ten_trinh_duyet"`
	UpdatedAt time.Time `json:"thoi_gian_cap_nhat"`
}

type CookieManager struct {
	filePath       string
	discordWebhook string
	client         *http.Client
	mu             sync.RWMutex
	session        *Session
	active         bool
	stopCh         chan struct{}
}

func NewCookieManager(filePath, discordWebhook string) *CookieManager {
	if filePath == "" {
		filePath = "zalo_session.json"
	}
	m := &CookieManager{
		filePath:       filePath,
		discordWebhook: discordWebhook,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		stopCh: make(chan struct{}),
	}
	_ = m.LoadSession()
	return m
}

func (m *CookieManager) SaveSession(cookie, secretKey, imei string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := &Session{
		Cookie:    cookie,
		SecretKey: secretKey,
		IMEI:      imei,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36",
		UpdatedAt: time.Now(),
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	if err := os.WriteFile(m.filePath, data, 0644); err != nil {
		return fmt.Errorf("write session: %w", err)
	}

	m.session = s
	m.active = true
	log.Println("[CookieManager] Session saved")
	return nil
}

func (m *CookieManager) loadSessionUnlocked() error {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return errors.New("session file not found")
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return errors.New("session json invalid")
	}

	m.session = &s
	return nil
}

func (m *CookieManager) LoadSession() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadSessionUnlocked()
}

func (m *CookieManager) GetCookie() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_ = m.loadSessionUnlocked()
	if m.session == nil || m.session.Cookie == "" {
		return "", errors.New("cookie not initialized")
	}
	return m.session.Cookie, nil
}

func (m *CookieManager) IsAlive() bool {
	cookie, err := m.GetCookie()
	if err != nil {
		m.setActive(false)
		return false
	}

	req, err := http.NewRequest(http.MethodGet, "https://profile-wpa.chat.zalo.me/api/social/profile/me?zpw_ver=631&zpw_type=30", nil)
	if err != nil {
		m.setActive(false)
		return false
	}

	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36")

	resp, err := m.client.Do(req)
	if err != nil {
		return m.active
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var zr struct {
		ErrorCode int `json:"error_code"`
	}
	_ = json.Unmarshal(bodyBytes, &zr)

	if resp.StatusCode == http.StatusOK && zr.ErrorCode == 0 {
		m.setActive(true)
		return true
	}

	log.Printf("[CookieManager] Session expired (HTTP %d, code %d)", resp.StatusCode, zr.ErrorCode)
	m.setActive(false)
	m.AlertDiscord()
	return false
}

func (m *CookieManager) setActive(alive bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = alive
}

func (m *CookieManager) StartHealthCheck(interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				_ = m.IsAlive()
			case <-m.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

func (m *CookieManager) AlertDiscord() {
	if m.discordWebhook == "" {
		return
	}

	payload := map[string]interface{}{
		"content": "**[CẢNH BÁO BCN] COOKIE ZALO ĐÃ HẾT HẠN!**\nVui lòng quét mã QR hoặc nạp Cookie mới trên Web Dashboard.",
	}

	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, m.discordWebhook, bytes.NewBuffer(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}
