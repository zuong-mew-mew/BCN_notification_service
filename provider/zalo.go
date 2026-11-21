package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bcn-noti-service/zalo_bot"
)

type Mention struct {
	Pos int    `json:"pos"`
	Len int    `json:"len"`
	UID string `json:"uid"`
}

type Member struct {
	FullName string `json:"ho_va_ten"`
	ZaloUID  string `json:"zalo_uid"`
}

type MemberCache struct {
	Mentors []Member `json:"mentors"`
	Members []Member `json:"members"`
}

type ZaloProvider struct {
	cookieMgr      *zalo_bot.CookieManager
	botURL         string
	defaultGroupID string
	cacheFile      string
	client         *http.Client
	cacheMu        sync.RWMutex
	cache          *MemberCache
}

func NewZaloProvider(cookieMgr *zalo_bot.CookieManager, botURL, defaultGroupID string) *ZaloProvider {
	p := &ZaloProvider{
		cookieMgr:      cookieMgr,
		botURL:         botURL,
		defaultGroupID: defaultGroupID,
		cacheFile:      "members_cache.json",
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
	_ = p.LoadCache()
	return p
}

func (z *ZaloProvider) SyncMemberCache(groupID string) (*MemberCache, error) {
	if os.Getenv("MOCK_ENABLED") == "true" {
		log.Println("[Zalo] MOCK_ENABLED=true: using members_cache.json")
		_ = z.LoadCache()
		return z.cache, nil
	}

	targetGroup := groupID
	if targetGroup == "" {
		targetGroup = z.defaultGroupID
	}
	if targetGroup == "" {
		return nil, errors.New("missing zalo group id")
	}

	if z.cookieMgr == nil {
		return nil, errors.New("missing CookieManager")
	}

	cookie, err := z.cookieMgr.GetCookie()
	if err != nil {
		return nil, fmt.Errorf("cookie error: %w", err)
	}

	apiURL := fmt.Sprintf("https://group-wpa.chat.zalo.me/api/group/getgridinfo?grid=%s", url.QueryEscape(targetGroup))
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36")

	resp, err := z.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http error: %d", resp.StatusCode)
	}

	raw, _ := io.ReadAll(resp.Body)

	var res struct {
		Error int    `json:"error"`
		Msg   string `json:"message"`
		Data  struct {
			GroupInfo struct {
				Admins []string `json:"admin_ids"`
			} `json:"group_info"`
			MemInfo []struct {
				UID      string `json:"userId"`
				ZaloName string `json:"zaloName"`
				DName    string `json:"dName"`
			} `json:"mem_info"`
		} `json:"data"`
	}

	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("json parse error: %w", err)
	}

	if res.Error != 0 {
		return nil, fmt.Errorf("zalo api error code %d: %s", res.Error, res.Msg)
	}

	adminMap := make(map[string]bool)
	for _, adminID := range res.Data.GroupInfo.Admins {
		adminMap[adminID] = true
	}

	newCache := &MemberCache{
		Mentors: []Member{},
		Members: []Member{},
	}

	for _, m := range res.Data.MemInfo {
		displayName := m.DName
		if displayName == "" {
			displayName = m.ZaloName
		}
		if displayName == "" {
			displayName = fmt.Sprintf("Thành viên %s", m.UID)
		}

		item := Member{
			FullName: displayName,
			ZaloUID:  m.UID,
		}

		if adminMap[m.UID] {
			newCache.Mentors = append(newCache.Mentors, item)
		} else {
			newCache.Members = append(newCache.Members, item)
		}
	}

	data, err := json.MarshalIndent(newCache, "", "  ")
	if err == nil {
		_ = os.WriteFile(z.cacheFile, data, 0644)
	}

	z.cacheMu.Lock()
	z.cache = newCache
	z.cacheMu.Unlock()

	return newCache, nil
}

func (z *ZaloProvider) LoadCache() error {
	z.cacheMu.Lock()
	defer z.cacheMu.Unlock()

	data, err := os.ReadFile(z.cacheFile)
	if err != nil {
		return err
	}

	var c MemberCache
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}

	z.cache = &c
	return nil
}

func (z *ZaloProvider) CheckRole(userUID string) (role string, fullName string) {
	z.cacheMu.RLock()
	defer z.cacheMu.RUnlock()

	if z.cache == nil {
		return "user", ""
	}

	for _, m := range z.cache.Mentors {
		if m.ZaloUID == userUID {
			return "mentor", m.FullName
		}
	}

	for _, m := range z.cache.Members {
		if m.ZaloUID == userUID {
			return "user", m.FullName
		}
	}

	return "outsider", ""
}

func (z *ZaloProvider) UpsertMember(uid, name string, isMentor bool) {
	z.cacheMu.Lock()
	defer z.cacheMu.Unlock()

	if z.cache == nil {
		z.cache = &MemberCache{
			Mentors: []Member{},
			Members: []Member{},
		}
	}

	var newMentors []Member
	for _, m := range z.cache.Mentors {
		if m.ZaloUID != uid {
			newMentors = append(newMentors, m)
		}
	}
	var newMembers []Member
	for _, m := range z.cache.Members {
		if m.ZaloUID != uid {
			newMembers = append(newMembers, m)
		}
	}

	item := Member{FullName: name, ZaloUID: uid}
	if isMentor {
		newMentors = append(newMentors, item)
	} else {
		newMembers = append(newMembers, item)
	}

	z.cache.Mentors = newMentors
	z.cache.Members = newMembers

	data, err := json.MarshalIndent(z.cache, "", "  ")
	if err == nil {
		_ = os.WriteFile(z.cacheFile, data, 0644)
	}
}

func (z *ZaloProvider) GetCache() *MemberCache {
	z.cacheMu.RLock()
	defer z.cacheMu.RUnlock()
	return z.cache
}

func (z *ZaloProvider) SendToUser(userID, content string) error {
	if strings.TrimSpace(userID) == "" {
		return errors.New("missing user id")
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("message content cannot be empty")
	}

	if os.Getenv("MOCK_ENABLED") == "true" {
		log.Printf("[Zalo Mock] SendToUser %s: %s", userID, content)
		return nil
	}

	if z.botURL != "" {
		return z.dispatchViaBot([]string{}, []string{userID}, content, false)
	}

	return z.sendDirectUser(userID, content)
}

func (z *ZaloProvider) SendToGroup(groupID, content string, tagAll bool) error {
	target := groupID
	if strings.TrimSpace(target) == "" {
		target = z.defaultGroupID
	}
	if strings.TrimSpace(target) == "" {
		return errors.New("missing group id")
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("message content cannot be empty")
	}

	if os.Getenv("MOCK_ENABLED") == "true" {
		log.Printf("[Zalo Mock] SendToGroup %s (TagAll: %v): %s", target, tagAll, content)
		return nil
	}

	if z.botURL != "" {
		return z.dispatchViaBot([]string{target}, []string{}, content, tagAll)
	}

	return z.sendGroupScript(target, content, tagAll)
}

func (z *ZaloProvider) SendGroupTagAll(groupID, content string) error {
	return z.SendToGroup(groupID, content, true)
}

func (z *ZaloProvider) sendGroupScript(groupID, content string, tagAll bool) error {
	tagStr := "false"
	if tagAll {
		tagStr = "true"
	}
	cmd := exec.Command("node", "send_msg.js", groupID, content, tagStr)
	cmd.Dir = filepath.Join(".", "zalo_sync")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("zalo send error: %v | %s", err, string(output))
	}
	log.Printf("[Zalo] Message sent to %s (tagAll=%v)", groupID, tagAll)
	return nil
}

func (z *ZaloProvider) sendDirectUser(userID, content string) error {
	if z.cookieMgr == nil {
		return errors.New("missing CookieManager")
	}

	cookie, err := z.cookieMgr.GetCookie()
	if err != nil {
		return fmt.Errorf("cookie error: %w", err)
	}

	form := url.Values{}
	form.Set("toid", userID)
	form.Set("message", content)
	form.Set("clientId", fmt.Sprintf("%d", time.Now().UnixMilli()))

	req, err := http.NewRequest(http.MethodPost, "https://chat.zalo.me/api/message/sms", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36")

	resp, err := z.client.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("zalo http error %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("[Zalo] Direct message sent to %s", userID)
	return nil
}

func (z *ZaloProvider) dispatchViaBot(groups, users []string, content string, tagAll bool) error {
	data := map[string]interface{}{
		"groupIds":  groups,
		"userIds":   users,
		"message":   content,
		"tagAll":    tagAll && len(groups) > 0,
		"tagFormat": "[@all]",
		"mode":      "personal",
	}

	buf, err := json.Marshal(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, z.botURL, bytes.NewBuffer(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := z.client.Do(req)
	if err != nil {
		return fmt.Errorf("bot dispatch error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bot returned %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[Zalo] Bot dispatched (groups=%v, users=%v)", groups, users)
	return nil
}
