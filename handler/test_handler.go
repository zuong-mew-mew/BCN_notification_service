package handler

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"bcn-noti-service/zalo_bot"
)

var paramCipherKey = []byte("3FC4F0D2AB50057BCE0D90D9187A22B1")

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padText...)
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	length := len(data)
	if length == 0 {
		return nil, fmt.Errorf("empty data")
	}
	padding := int(data[length-1])
	if padding > length || padding == 0 {
		return nil, fmt.Errorf("invalid padding")
	}
	for _, b := range data[length-padding:] {
		if int(b) != padding {
			return nil, fmt.Errorf("invalid padding byte")
		}
	}
	return data[:length-padding], nil
}

func aesEncryptCBC(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, block.BlockSize())
	iv := make([]byte, block.BlockSize())
	mode := cipher.NewCBCEncrypter(block, iv)
	ciphertext := make([]byte, len(padded))
	mode.CryptBlocks(ciphertext, padded)
	return ciphertext, nil
}

func aesDecryptCBC(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("ciphertext not multiple of block size")
	}
	iv := make([]byte, block.BlockSize())
	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)
	return pkcs7Unpad(plaintext)
}

func md5Hex(data string) string {
	h := md5.Sum([]byte(data))
	return hex.EncodeToString(h[:])
}

func randomHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

func splitEvenOdd(s string) (even []rune, odd []rune) {
	runes := []rune(s)
	for i, r := range runes {
		if i%2 == 0 {
			even = append(even, r)
		} else {
			odd = append(odd, r)
		}
	}
	return even, odd
}

type ParamCipher struct {
	Zcid       string
	ZcidExt    string
	EncryptKey string
	EncVer     string
}

func NewParamCipher(apiType, imei string, firstLaunchTime int64) (*ParamCipher, error) {
	plain := fmt.Sprintf("%s,%s,%d", apiType, imei, firstLaunchTime)
	encBytes, err := aesEncryptCBC(paramCipherKey, []byte(plain))
	if err != nil {
		return nil, err
	}
	zcid := strings.ToUpper(hex.EncodeToString(encBytes))
	zcidExt := randomHex(10)

	md5Upper := strings.ToUpper(md5Hex(zcidExt))
	nEven, _ := splitEvenOdd(md5Upper)
	aEven, aOdd := splitEvenOdd(zcid)

	for i, j := 0, len(aOdd)-1; i < j; i, j = i+1, j-1 {
		aOdd[i], aOdd[j] = aOdd[j], aOdd[i]
	}

	key := fmt.Sprintf("%s%s%s",
		string(nEven[:min(8, len(nEven))]),
		string(aEven[:min(12, len(aEven))]),
		string(aOdd[:min(12, len(aOdd))]),
	)

	return &ParamCipher{
		Zcid:       zcid,
		ZcidExt:    zcidExt,
		EncryptKey: key,
		EncVer:     "v2",
	}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (pc *ParamCipher) SignKey(tag string, params map[string]string) string {
	var keys []string
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString("zsecure")
	sb.WriteString(tag)
	for _, k := range keys {
		sb.WriteString(params[k])
	}
	return md5Hex(sb.String())
}

func (pc *ParamCipher) BuildParams(apiType, apiVersion, tag string, payload map[string]interface{}) (map[string]string, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	enc, err := aesEncryptCBC([]byte(pc.EncryptKey), jsonBytes)
	if err != nil {
		return nil, err
	}
	b64Enc := base64.StdEncoding.EncodeToString(enc)

	m := map[string]string{
		"zcid":           pc.Zcid,
		"zcid_ext":       pc.ZcidExt,
		"enc_ver":        pc.EncVer,
		"params":         b64Enc,
		"type":           apiType,
		"client_version": apiVersion,
	}
	m["signkey"] = pc.SignKey(tag, m)
	return m, nil
}

func (pc *ParamCipher) DecryptResponse(b64Data string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64Data))
	if err != nil {
		return "", err
	}
	pt, err := aesDecryptCBC([]byte(pc.EncryptKey), raw)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

type GroupMember struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	Avatar    string `json:"avatar,omitempty"`
	IsAdmin   bool   `json:"is_admin"`
	IsCreator bool   `json:"is_creator"`
}

type GroupItem struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	IsTest    bool          `json:"is_test"`
	IsPrivate bool          `json:"is_private"`
	Source    string        `json:"source"`
	Badge     string        `json:"badge,omitempty"`
	MemCount  int           `json:"mem_count,omitempty"`
	Members   []GroupMember `json:"members,omitempty"`
}

var (
	testGroupsLock sync.RWMutex
	testGroupsList = []GroupItem{
		{
			ID:        "4025169182418597977",
			Name:      "Ban Công Nghệ • School Life Stories (Nhóm Thật BCN)",
			IsTest:    true,
			IsPrivate: true,
			Source:    "official",
			Badge:     "NHÓM THẬT",
			MemCount:  34,
		},
	}
)

func loadCustomGroups() {
	testGroupsLock.Lock()
	defer testGroupsLock.Unlock()

	data, err := os.ReadFile("group_zalo.json")
	if err == nil {
		var list []GroupItem
		if json.Unmarshal(data, &list) == nil && len(list) > 0 {
			testGroupsList = list
			log.Printf("[Groups] Đã tải %d nhóm thật từ group_zalo.json", len(list))
		}
	}
}

func triggerZaloSyncScript() {
	cmd := exec.Command("node", "sync_groups.js")
	cmd.Dir = filepath.Join(".", "zalo_sync")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[ZaloSync] Lỗi chạy sync: %v | %s", err, string(output))
	} else {
		log.Printf("[ZaloSync] Đã đồng bộ Zalo API thành công!")
	}
	loadCustomGroups()
}

func sendZaloMessageViaScript(groupID, message string, tagAll bool) (bool, string, string, error) {
	tagStr := "false"
	if tagAll {
		tagStr = "true"
	}
	cmd := exec.Command("node", "send_msg.js", groupID, message, tagStr)
	cmd.Dir = filepath.Join(".", "zalo_sync")
	output, err := cmd.CombinedOutput()
	outStr := string(output)
	if err != nil {
		return false, "", outStr, fmt.Errorf("%v: %s", err, outStr)
	}

	idx := strings.LastIndex(outStr, `{"success":`)
	if idx >= 0 {
		var res struct {
			Success bool `json:"success"`
			Result  struct {
				Message struct {
					MsgID string `json:"msgId"`
				} `json:"message"`
			} `json:"result"`
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(outStr[idx:]), &res) == nil {
			if res.Success {
				msgID := res.Result.Message.MsgID
				return true, msgID, outStr, nil
			}
			return false, "", outStr, fmt.Errorf("%s", res.Error)
		}
	}
	return true, "", outStr, nil
}

func saveCustomGroups() {
	data, err := json.MarshalIndent(testGroupsList, "", "  ")
	if err == nil {
		_ = os.WriteFile("group_zalo.json", data, 0644)
	}
}

func extractZpdid(cookie string) string {
	re := regexp.MustCompile(`zpdid=([^;\s]+)`)
	m := re.FindStringSubmatch(cookie)
	if len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		return strings.TrimSpace(m[1])
	}
	return "41F-arRrhZiH6PQKK_N4E14HbP9IzC0y"
}

func fetchRealZaloGroups(cookie string, imei string) []GroupItem {
	if strings.TrimSpace(cookie) == "" {
		return nil
	}
	if imei == "" {
		imei = extractZpdid(cookie)
	}

	pc, err := NewParamCipher("30", imei, time.Now().UnixMilli())
	if err != nil {
		return nil
	}

	client := &http.Client{Timeout: 10 * time.Second}

	pMap, err := pc.BuildParams("30", "645", "getlg", map[string]interface{}{})
	if err != nil {
		return nil
	}

	uv := url.Values{}
	for k, v := range pMap {
		uv.Set(k, v)
	}
	uv.Set("zpw_ver", "645")
	uv.Set("zpw_type", "30")

	endpoint := "https://tt-group-wpa.chat.zalo.me/api/group/getlg/v4?" + uv.Encode()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil
	}

	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Origin", "https://chat.zalo.me")
	req.Header.Set("Referer", "https://chat.zalo.me/")

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var zResp struct {
		ErrorCode int    `json:"error_code"`
		Data      string `json:"data"`
	}
	if err := json.Unmarshal(body, &zResp); err != nil || zResp.ErrorCode != 0 || zResp.Data == "" {
		return nil
	}

	decryptedJSON, err := pc.DecryptResponse(zResp.Data)
	if err != nil {
		return nil
	}

	var parsedData struct {
		GridVerMap map[string]interface{} `json:"gridVerMap"`
		GridList   []struct {
			Grid     string `json:"grid"`
			Name     string `json:"name"`
			TotalMem int    `json:"total_mem"`
		} `json:"gridList"`
	}
	_ = json.Unmarshal([]byte(decryptedJSON), &parsedData)

	var result []GroupItem

	for gridID := range parsedData.GridVerMap {
		if strings.TrimSpace(gridID) == "" {
			continue
		}
		result = append(result, GroupItem{
			ID:        gridID,
			Name:      fmt.Sprintf("Nhóm Zalo (%s)", gridID),
			IsTest:    false,
			IsPrivate: true,
			Source:    "zalo_live",
			Badge:     "ZALO THẬT",
			MemCount:  0,
		})
	}

	for _, g := range parsedData.GridList {
		if strings.TrimSpace(g.Grid) == "" {
			continue
		}
		exists := false
		for i, ex := range result {
			if ex.ID == g.Grid {
				result[i].Name = g.Name
				if g.TotalMem > 0 {
					result[i].MemCount = g.TotalMem
				}
				exists = true
				break
			}
		}
		if !exists {
			result = append(result, GroupItem{
				ID:        g.Grid,
				Name:      g.Name,
				IsTest:    false,
				IsPrivate: true,
				Source:    "zalo_live",
				Badge:     "ZALO THẬT",
				MemCount:  g.TotalMem,
			})
		}
	}

	return result
}

func fetchRealGroupMembers(cookie, groupID string) ([]GroupMember, string, int, error) {
	if strings.TrimSpace(cookie) == "" {
		return nil, "", 0, fmt.Errorf("cookie không khả dụng")
	}

	imei := extractZpdid(cookie)
	pc, _ := NewParamCipher("30", imei, time.Now().UnixMilli())

	client := &http.Client{Timeout: 10 * time.Second}

	urlsToTry := []string{
		fmt.Sprintf("https://group-wpa.chat.zalo.me/api/group/getgridinfo?grid=%s", url.QueryEscape(groupID)),
		fmt.Sprintf("https://tt-group-wpa.chat.zalo.me/api/group/getgridinfo?grid=%s&zpw_ver=645&zpw_type=30", url.QueryEscape(groupID)),
		fmt.Sprintf("https://profile-wpa.chat.zalo.me/api/social/group/members?grid=%s&zpw_ver=645&zpw_type=30", url.QueryEscape(groupID)),
	}

	for _, endpoint := range urlsToTry {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			continue
		}

		req.Header.Set("Cookie", cookie)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Origin", "https://chat.zalo.me")
		req.Header.Set("Referer", "https://chat.zalo.me/")

		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var encResp struct {
			ErrorCode int         `json:"error_code"`
			Error     int         `json:"error"`
			Data      interface{} `json:"data"`
		}
		if json.Unmarshal(body, &encResp) == nil {
			if strData, ok := encResp.Data.(string); ok && strData != "" && pc != nil {
				if dec, errDec := pc.DecryptResponse(strData); errDec == nil {
					body = []byte(dec)
				}
			}
		}

		var zResp struct {
			Error int    `json:"error"`
			Msg   string `json:"message"`
			Data  struct {
				GroupInfo struct {
					Name      string   `json:"name"`
					CreatorID string   `json:"creator_id"`
					Admins    []string `json:"admin_ids"`
					TotalMem  int      `json:"total_mem"`
				} `json:"group_info"`
				MemInfo []struct {
					UID      string `json:"userId"`
					ZaloName string `json:"zaloName"`
					DName    string `json:"dName"`
					Avatar   string `json:"avatar"`
				} `json:"mem_info"`
				GridInfoMap map[string]struct {
					Name        string   `json:"name"`
					CreatorID   string   `json:"creatorId"`
					TotalMember int      `json:"totalMember"`
					AdminIDs    []string `json:"adminIds"`
					MemVerList  []string `json:"memVerList"`
				} `json:"gridInfoMap"`
			} `json:"data"`
			GridInfoMap map[string]struct {
				Name        string   `json:"name"`
				CreatorID   string   `json:"creatorId"`
				TotalMember int      `json:"totalMember"`
				AdminIDs    []string `json:"adminIds"`
				MemVerList  []string `json:"memVerList"`
			} `json:"gridInfoMap"`
		}

		if err := json.Unmarshal(body, &zResp); err == nil {
			groupName := zResp.Data.GroupInfo.Name
			totalMem := zResp.Data.GroupInfo.TotalMem
			creatorID := zResp.Data.GroupInfo.CreatorID

			adminMap := make(map[string]bool)
			for _, adminID := range zResp.Data.GroupInfo.Admins {
				adminMap[adminID] = true
			}

			gInfoMap := zResp.Data.GridInfoMap
			if len(gInfoMap) == 0 {
				gInfoMap = zResp.GridInfoMap
			}
			if gInfo, ok := gInfoMap[groupID]; ok {
				if groupName == "" {
					groupName = gInfo.Name
				}
				if totalMem == 0 {
					totalMem = gInfo.TotalMember
				}
				if creatorID == "" {
					creatorID = gInfo.CreatorID
				}
				for _, a := range gInfo.AdminIDs {
					adminMap[a] = true
				}
			}

			var members []GroupMember
			for _, m := range zResp.Data.MemInfo {
				name := m.DName
				if name == "" {
					name = m.ZaloName
				}
				if name == "" {
					name = fmt.Sprintf("Thành viên %s", m.UID)
				}
				members = append(members, GroupMember{
					UID:       m.UID,
					Name:      name,
					Avatar:    m.Avatar,
					IsAdmin:   adminMap[m.UID],
					IsCreator: m.UID == creatorID,
				})
			}

			if len(members) == 0 && len(gInfoMap) > 0 {
				if gInfo, ok := gInfoMap[groupID]; ok && len(gInfo.MemVerList) > 0 {
					for _, item := range gInfo.MemVerList {
						parts := strings.Split(item, "_")
						uid := parts[0]
						if uid == "" {
							continue
						}
						members = append(members, GroupMember{
							UID:       uid,
							Name:      fmt.Sprintf("Thành viên Zalo (%s)", uid),
							IsAdmin:   adminMap[uid],
							IsCreator: uid == creatorID,
						})
					}
				}
			}

			if len(members) > 0 {
				if totalMem == 0 {
					totalMem = len(members)
				}
				return members, groupName, totalMem, nil
			}
		}
	}

	return nil, "", 0, fmt.Errorf("không thể lấy thông tin nhóm từ Zalo (phiên có thể đã hết hạn)")
}

func SetupTestRoutes(cfg ZaloConfig, cookieMgr *zalo_bot.CookieManager) {
	loadCustomGroups()

	http.HandleFunc("/api/test/bot/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		if cookieMgr == nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"has_cookie": false,
				"alive":      false,
				"message":    "Chưa khởi tạo bộ quản lý Cookie",
			})
			return
		}

		cookie, err := cookieMgr.GetCookie()
		if err != nil || strings.TrimSpace(cookie) == "" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"has_cookie": false,
				"alive":      false,
				"message":    "Chưa có thông tin Cookie trong hệ thống",
			})
			return
		}

		cookieSnippet := cookie
		if len(cookieSnippet) > 40 {
			cookieSnippet = cookieSnippet[:35] + "..."
		}

		req, err := http.NewRequest(http.MethodGet, "https://profile-wpa.chat.zalo.me/api/social/profile/me?zpw_ver=631&zpw_type=30", nil)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"has_cookie":     true,
				"alive":          false,
				"cookie_snippet": cookieSnippet,
				"message":        "Lỗi khởi tạo request",
			})
			return
		}

		req.Header.Set("Cookie", cookie)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Origin", "https://chat.zalo.me")
		req.Header.Set("Referer", "https://chat.zalo.me/")

		client := &http.Client{Timeout: 8 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"has_cookie":     true,
				"alive":          false,
				"cookie_snippet": cookieSnippet,
				"message":        fmt.Sprintf("Không thể kết nối máy chủ Zalo: %v", err),
			})
			return
		}
		defer resp.Body.Close()

		bodyBytes, _ := io.ReadAll(resp.Body)
		var zaloResp struct {
			ErrorCode    int         `json:"error_code"`
			ErrorMessage string      `json:"error_message"`
			Data         interface{} `json:"data"`
		}
		_ = json.Unmarshal(bodyBytes, &zaloResp)

		userId := ""
		displayName := "Ban Công Nghệ (Zalo Bot)"
		avatar := "/bcn_logo.png"

		reUid := regexp.MustCompile(`zpw_sek=[^.]+\.(\d+)`)
		mUid := reUid.FindStringSubmatch(cookie)
		if len(mUid) > 1 {
			userId = mUid[1]
			displayName = fmt.Sprintf("Zalo Bot (UID %s)", userId)
		}

		if dMap, ok := zaloResp.Data.(map[string]interface{}); ok {
			if u, ok := dMap["userId"].(string); ok && u != "" {
				userId = u
			}
			if dn, ok := dMap["displayName"].(string); ok && dn != "" {
				displayName = dn
			}
			if av, ok := dMap["avatar"].(string); ok && av != "" {
				avatar = av
			}
		}

		isAlive := resp.StatusCode == http.StatusOK && (zaloResp.ErrorCode == 0 || userId != "")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"has_cookie":     true,
			"alive":          isAlive,
			"http_status":    resp.StatusCode,
			"error_code":     zaloResp.ErrorCode,
			"error_message":  zaloResp.ErrorMessage,
			"user_id":        userId,
			"uid":            userId,
			"display_name":   displayName,
			"name":           displayName,
			"avatar":         avatar,
			"cookie_snippet": cookieSnippet,
		})
	})

	http.HandleFunc("/api/test/bot/cookie", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Cookie string `json:"cookie"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Cookie) == "" {
			http.Error(w, `{"error":"Vui lòng nhập chuỗi Cookie hợp lệ!"}`, http.StatusBadRequest)
			return
		}

		cleanCookie := strings.TrimSpace(req.Cookie)
		if cookieMgr != nil {
			_ = cookieMgr.SaveSession(cleanCookie, "", "")
		}

		isAlive := false
		var userId, displayName, avatar, errDetail string
		reqZalo, err := http.NewRequest(http.MethodGet, "https://profile-wpa.chat.zalo.me/api/social/profile/me?zpw_ver=631&zpw_type=30", nil)
		if err == nil {
			reqZalo.Header.Set("Cookie", cleanCookie)
			reqZalo.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36")
			reqZalo.Header.Set("Origin", "https://chat.zalo.me")
			reqZalo.Header.Set("Referer", "https://chat.zalo.me/")

			client := &http.Client{Timeout: 8 * time.Second}
			resp, errDo := client.Do(reqZalo)
			if errDo == nil {
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				var zr struct {
					ErrorCode    int    `json:"error_code"`
					ErrorMessage string `json:"error_message"`
					Data         struct {
						UserId      string `json:"userId"`
						DisplayName string `json:"displayName"`
						Avatar      string `json:"avatar"`
					} `json:"data"`
				}
				_ = json.Unmarshal(body, &zr)
				reUid := regexp.MustCompile(`zpw_sek=[^.]+\.(\d+)`)
				mUid := reUid.FindStringSubmatch(cleanCookie)
				if len(mUid) > 1 {
					userId = mUid[1]
					displayName = fmt.Sprintf("Zalo Bot (UID %s)", userId)
					avatar = "/bcn_logo.png"
				}

				if resp.StatusCode == http.StatusOK && zr.ErrorCode == 0 {
					isAlive = true
					if zr.Data.UserId != "" {
						userId = zr.Data.UserId
					}
					if zr.Data.DisplayName != "" {
						displayName = zr.Data.DisplayName
					}
					if zr.Data.Avatar != "" {
						avatar = zr.Data.Avatar
					}
				} else if userId != "" {
					isAlive = true
				} else {
					errDetail = fmt.Sprintf("Zalo báo mã lỗi: %d (%s)", zr.ErrorCode, zr.ErrorMessage)
				}
			}
		}

		go triggerZaloSyncScript()

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":      true,
			"alive":        isAlive,
			"user_id":      userId,
			"display_name": displayName,
			"avatar":       avatar,
			"err_detail":   errDetail,
		})
	})

	http.HandleFunc("/api/test/groups", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		if r.URL.Query().Get("refresh") == "true" || len(testGroupsList) <= 1 {
			triggerZaloSyncScript()
		}

		testGroupsLock.RLock()
		groups := make([]GroupItem, len(testGroupsList))
		copy(groups, testGroupsList)
		testGroupsLock.RUnlock()

		hasCookie := false
		if cookieMgr != nil {
			cookie, err := cookieMgr.GetCookie()
			if err == nil && strings.TrimSpace(cookie) != "" {
				hasCookie = true
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"has_cookie":   hasCookie,
			"cookie_alive": true,
			"total":        len(groups),
			"groups":       groups,
		})
	})

	http.HandleFunc("/api/test/groups/members", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		groupID := strings.TrimSpace(r.URL.Query().Get("id"))
		if groupID == "" {
			http.Error(w, `{"error":"Thiếu tham số ID nhóm (id)"}`, http.StatusBadRequest)
			return
		}

		cookie := ""
		if cookieMgr != nil {
			cookie, _ = cookieMgr.GetCookie()
		}

		members, groupName, totalMem, err := fetchRealGroupMembers(cookie, groupID)
		if err == nil && len(members) > 0 {
			testGroupsLock.Lock()
			for i, g := range testGroupsList {
				if g.ID == groupID {
					if groupName != "" {
						testGroupsList[i].Name = groupName
					}
					testGroupsList[i].MemCount = totalMem
					testGroupsList[i].Members = members
					break
				}
			}
			saveCustomGroups()
			testGroupsLock.Unlock()

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success":    true,
				"group_id":   groupID,
				"group_name": groupName,
				"total":      totalMem,
				"members":    members,
				"source":     "zalo_live",
			})
			return
		}

		cachedData, _ := os.ReadFile("members_cache.json")
		var cache struct {
			Mentors []struct {
				HoVaTen string `json:"ho_va_ten"`
				ZaloUID string `json:"zalo_uid"`
			} `json:"mentors"`
			Members []struct {
				HoVaTen string `json:"ho_va_ten"`
				ZaloUID string `json:"zalo_uid"`
			} `json:"members"`
		}
		_ = json.Unmarshal(cachedData, &cache)

		var fallbackMembers []GroupMember
		for _, m := range cache.Mentors {
			fallbackMembers = append(fallbackMembers, GroupMember{
				UID:       m.ZaloUID,
				Name:      m.HoVaTen,
				IsAdmin:   true,
				IsCreator: false,
			})
		}
		for _, m := range cache.Members {
			fallbackMembers = append(fallbackMembers, GroupMember{
				UID:       m.ZaloUID,
				Name:      m.HoVaTen,
				IsAdmin:   false,
				IsCreator: false,
			})
		}

		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"group_id":   groupID,
			"group_name": "Nhóm BCN (Từ danh bạ cache)",
			"total":      len(fallbackMembers),
			"members":    fallbackMembers,
			"source":     "cache",
			"note":       "Không kết nối được Zalo trực tiếp, đang hiển thị danh sách từ members_cache.json",
			"err_detail": errMsg,
		})
	})

	http.HandleFunc("/api/test/groups/add", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ID) == "" {
			http.Error(w, `{"error":"Vui lòng cung cấp ID nhóm hoặc đường link nhóm Zalo"}`, http.StatusBadRequest)
			return
		}

		inputVal := strings.TrimSpace(req.ID)
		resolvedID := inputVal

		reDigit := regexp.MustCompile(`\b(\d{15,22})\b`)
		if matches := reDigit.FindStringSubmatch(inputVal); len(matches) > 1 {
			resolvedID = matches[1]
		}

		name := strings.TrimSpace(req.Name)
		if name == "" {
			if strings.Contains(inputVal, "zalo.me") {
				name = "Nhóm Zalo (Tự bóc tách từ link)"
			} else {
				name = fmt.Sprintf("Nhóm Thử Nghiệm (%s)", resolvedID)
			}
		}

		cookie := ""
		if cookieMgr != nil {
			cookie, _ = cookieMgr.GetCookie()
		}
		members, realName, totalMem, errMem := fetchRealGroupMembers(cookie, resolvedID)
		if errMem == nil && realName != "" {
			name = realName
		}

		testGroupsLock.Lock()
		exists := false
		for i, g := range testGroupsList {
			if g.ID == resolvedID {
				testGroupsList[i].Name = name
				testGroupsList[i].Badge = "TÙY CHỌN"
				if len(members) > 0 {
					testGroupsList[i].MemCount = totalMem
					testGroupsList[i].Members = members
				}
				exists = true
				break
			}
		}
		if !exists {
			testGroupsList = append(testGroupsList, GroupItem{
				ID:        resolvedID,
				Name:      name,
				IsTest:    true,
				IsPrivate: true,
				Source:    "custom",
				Badge:     "TÙY CHỌN",
				MemCount:  totalMem,
				Members:   members,
			})
		}
		saveCustomGroups()
		testGroupsLock.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"resolved_id": resolvedID,
			"name":        name,
			"total_mem":   totalMem,
			"groups":      testGroupsList,
		})
	})

	http.HandleFunc("/api/test/groups/remove", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ID) == "" {
			http.Error(w, `{"error":"ID nhóm không được để trống"}`, http.StatusBadRequest)
			return
		}

		testGroupsLock.Lock()
		var newList []GroupItem
		for _, g := range testGroupsList {
			if g.ID != strings.TrimSpace(req.ID) || g.Source == "default" {
				newList = append(newList, g)
			}
		}
		testGroupsList = newList
		saveCustomGroups()
		testGroupsLock.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"groups":  testGroupsList,
		})
	})

	http.HandleFunc("/api/test/send", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			GroupID string `json:"group_id"`
			Message string `json:"message"`
			TagAll  bool   `json:"tag_all"`
			Mode    string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.GroupID) == "" || strings.TrimSpace(req.Message) == "" {
			http.Error(w, `{"error":"Vui lòng chọn ID nhóm và nhập nội dung tin nhắn!"}`, http.StatusBadRequest)
			return
		}

		startTime := time.Now()

		if req.Mode == "mock" {
			time.Sleep(50 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success":      true,
				"mode":         "mock",
				"http_status":  200,
				"latency_ms":   time.Since(startTime).Milliseconds(),
				"target_group": req.GroupID,
				"tag_all":      req.TagAll,
				"endpoint":     "mock://internal/zalo/sendmsg",
				"raw_response": `{"error_code": 0, "error_message": "Success", "data": {"msgId": "mock_123456789"}}`,
				"explanation":  "[MOCK] Tin nhắn giả lập đã được mô phỏng thành công trên máy chủ.",
			})
			return
		}

		if cookieMgr == nil {
			http.Error(w, `{"error":"Chưa khởi tạo bộ quản lý Cookie"}`, http.StatusInternalServerError)
			return
		}

		cookie, err := cookieMgr.GetCookie()
		if err != nil || strings.TrimSpace(cookie) == "" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success":      false,
				"mode":         "live",
				"error":        "Chưa có Cookie trong hệ thống! Vui lòng quét mã QR hoặc nạp Cookie mới.",
				"target_group": req.GroupID,
			})
			return
		}

		success, msgID, rawResp, errSend := sendZaloMessageViaScript(req.GroupID, req.Message, req.TagAll)
		latency := time.Since(startTime).Milliseconds()

		explanation := ""
		if success {
			explanation = fmt.Sprintf("Phát sóng thành công! Tin nhắn đã xuất hiện trên Zalo nhóm (MsgID: %s).", msgID)
		} else {
			explanation = fmt.Sprintf("Gửi tin chưa thành công: %v", errSend)
		}

		log.Printf("[TestDispatch] Nhóm %s | Success: %v (MsgID: %s)", req.GroupID, success, msgID)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":      success,
			"mode":         "live",
			"http_status":  200,
			"latency_ms":   latency,
			"endpoint":     "https://group-wpa.chat.zalo.me/api/group",
			"target_group": req.GroupID,
			"tag_all":      req.TagAll,
			"msg_id":       msgID,
			"raw_response": rawResp,
			"explanation":  explanation,
		})
	})
}
