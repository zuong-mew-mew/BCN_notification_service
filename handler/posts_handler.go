package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"bcn-noti-service/zalo_bot"
)

// Post đại diện cho một bài thông báo trong hệ thống
type Post struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Content        string     `json:"content"`
	AuthorUID      string     `json:"author_uid"`
	AuthorName     string     `json:"author_name"`
	AuthorRole     string     `json:"author_role"`
	Status         string     `json:"status"` // "pending", "approved", "rejected", "cancelled"
	TagAll         bool       `json:"tag_all"`
	ScheduledAt    *time.Time `json:"scheduled_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ApprovedByUID  string     `json:"approved_by_uid,omitempty"`
	ApprovedByName string     `json:"approved_by_name,omitempty"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty"`
	RejectedByUID  string     `json:"rejected_by_uid,omitempty"`
	RejectedByName string     `json:"rejected_by_name,omitempty"`
	RejectedAt     *time.Time `json:"rejected_at,omitempty"`
	RejectReason   string     `json:"reject_reason,omitempty"`
}

// KhoLuuBai: Bộ lưu trữ bài viết an toàn đa luồng
type KhoLuuBai struct {
	khoa sync.RWMutex
	ds   []Post
}

var khoBaiKhoiTao = &KhoLuuBai{
	ds: []Post{
		{
			ID:         "post_1",
			Title:      "Họp toàn Ban Công Nghệ rà soát tiến độ",
			Content:    "Tối nay 19:30 họp Ban Công Nghệ để rà soát tiến độ dự án tuần mới. Đề nghị mọi người có mặt đầy đủ.",
			AuthorUID:  "999999",
			AuthorName: "Nguyễn Văn A (Thành viên BCN)",
			AuthorRole: "user",
			Status:     "pending",
			TagAll:     true,
			CreatedAt:  time.Now().Add(-2 * time.Hour),
		},
		{
			ID:             "post_2",
			Title:          "Báo cáo tiến độ kỹ thuật Tuần 3",
			Content:        "Đã hoàn thành module giữ Cookie Zalo, cơ chế phân quyền RBAC và trích xuất danh bạ members_cache.json.",
			AuthorUID:      "111111",
			AuthorName:     "Trần Mentor (Key Bạc)",
			AuthorRole:     "mentor",
			Status:         "approved",
			TagAll:         true,
			CreatedAt:      time.Now().Add(-5 * time.Hour),
			ApprovedByUID:  "111111",
			ApprovedByName: "Trần Mentor (Key Bạc)",
			ApprovedAt:     func() *time.Time { t := time.Now().Add(-4 * time.Hour); return &t }(),
		},
	},
}

// SetupPostAndBotRoutes đăng ký các API xử lý bài viết và quản lý Bot Cookie
func SetupPostAndBotRoutes(cfg ZaloConfig, cookieMgr *zalo_bot.CookieManager) {

	// 1. GET /api/posts: Lấy danh sách bài viết theo phân quyền
	// - User: CHỈ lấy các bài do chính họ viết (author_uid == uid)
	// - Mentor: Lấy toàn bộ bài, hoặc lọc theo tab (pending / history)
	http.HandleFunc("/api/posts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		if r.Method == http.MethodPost {
			// Tạo bài viết mới
			var req struct {
				Title       string `json:"title"`
				Content     string `json:"content"`
				AuthorUID   string `json:"author_uid"`
				AuthorName  string `json:"author_name"`
				AuthorRole  string `json:"author_role"`
				TagAll      bool   `json:"tag_all"`
				ScheduledAt string `json:"scheduled_at"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Title) == "" {
				http.Error(w, `{"error":"Tiêu đề không được để trống"}`, http.StatusBadRequest)
				return
			}

			var schedTime *time.Time
			if strings.TrimSpace(req.ScheduledAt) != "" {
				parsed, err := time.Parse("2006-01-02T15:04", req.ScheduledAt)
				if err == nil {
					schedTime = &parsed
				} else {
					parsedRFC, errRFC := time.Parse(time.RFC3339, req.ScheduledAt)
					if errRFC == nil {
						schedTime = &parsedRFC
					}
				}
			}

			baiMoi := Post{
				ID:          fmt.Sprintf("post_%d", time.Now().UnixNano()),
				Title:       req.Title,
				Content:     req.Content,
				AuthorUID:   req.AuthorUID,
				AuthorName:  req.AuthorName,
				AuthorRole:  req.AuthorRole,
				Status:      "pending",
				TagAll:      req.TagAll,
				ScheduledAt: schedTime,
				CreatedAt:   time.Now(),
			}

			khoBaiKhoiTao.khoa.Lock()
			khoBaiKhoiTao.ds = append([]Post{baiMoi}, khoBaiKhoiTao.ds...)
			khoBaiKhoiTao.khoa.Unlock()

			_ = json.NewEncoder(w).Encode(baiMoi)
			return
		}

		// GET bài viết
		role := r.URL.Query().Get("role")
		uid := r.URL.Query().Get("uid")
		tab := r.URL.Query().Get("tab") // "pending" hoặc "history"

		khoBaiKhoiTao.khoa.RLock()
		defer khoBaiKhoiTao.khoa.RUnlock()

		var ketQua []Post

		if role == "mentor" {
			// Mentor: Xem rộng
			if tab == "pending" {
				for _, p := range khoBaiKhoiTao.ds {
					if p.Status == "pending" {
						ketQua = append(ketQua, p)
					}
				}
			} else if tab == "history" {
				for _, p := range khoBaiKhoiTao.ds {
					if p.Status == "approved" || p.Status == "rejected" || p.Status == "cancelled" {
						ketQua = append(ketQua, p)
					}
				}
			} else {
				ketQua = append(ketQua, khoBaiKhoiTao.ds...)
			}
		} else {
			// User bình thường: TUYỆT ĐỐI CHỈ XEM BÀI CỦA CHÍNH MÌNH
			for _, p := range khoBaiKhoiTao.ds {
				if p.AuthorUID == uid {
					ketQua = append(ketQua, p)
				}
			}
		}

		if ketQua == nil {
			ketQua = []Post{}
		}
		_ = json.NewEncoder(w).Encode(ketQua)
	})

	// 2. POST /api/posts/approve: Mentor phê duyệt bài viết và kích hoạt Bot
	http.HandleFunc("/api/posts/approve", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			PostID     string `json:"post_id"`
			MentorUID  string `json:"mentor_uid"`
			MentorName string `json:"mentor_name"`
			Role       string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"Dữ liệu không hợp lệ"}`, http.StatusBadRequest)
			return
		}

		// Kiểm tra quyền Mentor
		if req.Role != "mentor" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"403 Forbidden: Chỉ Mentor mới có quyền phê duyệt bài!"}`))
			return
		}

		khoBaiKhoiTao.khoa.Lock()
		var postCanDuyet *Post
		for i := range khoBaiKhoiTao.ds {
			if khoBaiKhoiTao.ds[i].ID == req.PostID {
				now := time.Now()
				khoBaiKhoiTao.ds[i].Status = "approved"
				khoBaiKhoiTao.ds[i].ApprovedByUID = req.MentorUID
				khoBaiKhoiTao.ds[i].ApprovedByName = req.MentorName
				khoBaiKhoiTao.ds[i].ApprovedAt = &now
				postCanDuyet = &khoBaiKhoiTao.ds[i]
				break
			}
		}
		khoBaiKhoiTao.khoa.Unlock()

		if postCanDuyet == nil {
			http.Error(w, `{"error":"Không tìm thấy bài viết"}`, http.StatusNotFound)
			return
		}

		// Bắn tin nhắn qua Bot Zalo vào nhóm
		var botMsg string
		if cfg.ZaloProvider != nil {
			noiDung := fmt.Sprintf("[%s] %s\n\n%s", postCanDuyet.Title, postCanDuyet.Content, "Duyệt bởi: "+req.MentorName)
			var errBot error
			if postCanDuyet.TagAll {
				errBot = cfg.ZaloProvider.SendGroupTagAll("", noiDung)
			} else {
				errBot = cfg.ZaloProvider.SendToGroup("", noiDung, false)
			}
			if errBot != nil {
				botMsg = fmt.Sprintf("Bot cảnh báo: %v", errBot)
			} else {
				botMsg = "Bot đã phát tin nhắn Zalo thành công vào nhóm BCN!"
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"post":    postCanDuyet,
			"bot_log": botMsg,
		})
	})

	// 3. POST /api/posts/reject: Mentor từ chối bài viết
	http.HandleFunc("/api/posts/reject", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			PostID     string `json:"post_id"`
			MentorUID  string `json:"mentor_uid"`
			MentorName string `json:"mentor_name"`
			Role       string `json:"role"`
			Reason     string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"Dữ liệu không hợp lệ"}`, http.StatusBadRequest)
			return
		}

		if req.Role != "mentor" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"403 Forbidden: Chỉ Mentor mới có quyền từ chối bài!"}`))
			return
		}

		khoBaiKhoiTao.khoa.Lock()
		var postCanTuChoi *Post
		for i := range khoBaiKhoiTao.ds {
			if khoBaiKhoiTao.ds[i].ID == req.PostID {
				now := time.Now()
				khoBaiKhoiTao.ds[i].Status = "rejected"
				khoBaiKhoiTao.ds[i].RejectedByUID = req.MentorUID
				khoBaiKhoiTao.ds[i].RejectedByName = req.MentorName
				khoBaiKhoiTao.ds[i].RejectedAt = &now
				khoBaiKhoiTao.ds[i].RejectReason = req.Reason
				postCanTuChoi = &khoBaiKhoiTao.ds[i]
				break
			}
		}
		khoBaiKhoiTao.khoa.Unlock()

		if postCanTuChoi == nil {
			http.Error(w, `{"error":"Không tìm thấy bài viết"}`, http.StatusNotFound)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"post":    postCanTuChoi,
		})
	})

	// 4. POST /api/posts/cancel: Hủy queue bài nộp hoặc hoàn tác duyệt nhầm
	http.HandleFunc("/api/posts/cancel", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			PostID string `json:"post_id"`
			UID    string `json:"uid"`
			Role   string `json:"role"`
			Action string `json:"action"` // "revert_pending" (hoàn tác về chờ duyệt) hoặc "cancel" (hủy bỏ)
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"Dữ liệu không hợp lệ"}`, http.StatusBadRequest)
			return
		}

		khoBaiKhoiTao.khoa.Lock()
		var targetPost *Post
		for i := range khoBaiKhoiTao.ds {
			if khoBaiKhoiTao.ds[i].ID == req.PostID {
				// Mentor có quyền can thiệp mọi bài. User chỉ được hủy bài do chính mình nộp và đang pending
				if req.Role != "mentor" && khoBaiKhoiTao.ds[i].AuthorUID != req.UID {
					khoBaiKhoiTao.khoa.Unlock()
					http.Error(w, `{"error":"403 Forbidden: Bạn không có quyền thao tác bài này!"}`, http.StatusForbidden)
					return
				}

				if req.Action == "revert_pending" {
					khoBaiKhoiTao.ds[i].Status = "pending"
					khoBaiKhoiTao.ds[i].ApprovedAt = nil
					khoBaiKhoiTao.ds[i].ApprovedByUID = ""
					khoBaiKhoiTao.ds[i].ApprovedByName = ""
				} else {
					khoBaiKhoiTao.ds[i].Status = "cancelled"
				}
				targetPost = &khoBaiKhoiTao.ds[i]
				break
			}
		}
		khoBaiKhoiTao.khoa.Unlock()

		if targetPost == nil {
			http.Error(w, `{"error":"Không tìm thấy bài viết"}`, http.StatusNotFound)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"post":    targetPost,
			"action":  req.Action,
		})
	})

	// 4. POST /api/bot/cookie: Nhận chuỗi Cookie thật từ Web Dashboard để nạp vào Bot
	http.HandleFunc("/api/bot/cookie", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Cookie string `json:"cookie"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Cookie) == "" {
			http.Error(w, `{"error":"Vui lòng dán chuỗi cookie hợp lệ!"}`, http.StatusBadRequest)
			return
		}

		cleanCookie := strings.TrimSpace(req.Cookie)
		if cookieMgr != nil {
			_ = cookieMgr.SaveSession(cleanCookie, "", "")
		}

		// Kiểm tra sống chết thực tế
		isAlive := false
		if cookieMgr != nil {
			isAlive = cookieMgr.IsAlive()
		}

		// Tự động kích hoạt đồng bộ trích xuất danh sách thành viên nhóm BCN
		var syncDetail string
		var countMentor, countMember int
		if cfg.ZaloProvider != nil {
			cacheMoi, errSync := cfg.ZaloProvider.SyncMemberCache("")
			if errSync == nil && cacheMoi != nil {
				countMentor = len(cacheMoi.Mentors)
				countMember = len(cacheMoi.Members)
				syncDetail = fmt.Sprintf("Đã trích xuất thành công %d Mentor và %d Thành viên vào members_cache.json!", countMentor, countMember)
			} else {
				syncDetail = fmt.Sprintf("Lưu cookie thành công nhưng chưa trích xuất được nhóm: %v", errSync)
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":      true,
			"cookie_alive": isAlive,
			"sync_detail":  syncDetail,
			"mentors":      countMentor,
			"members":      countMember,
		})
	})

	// 5. GET /api/bot/status: Kiểm tra trạng thái hiện tại của Bot Cookie
	http.HandleFunc("/api/bot/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		isAlive := false
		hasCookie := false
		if cookieMgr != nil {
			c, err := cookieMgr.GetCookie()
			if err == nil && c != "" && !strings.Contains(c, "KHOA_BI_MAT_MAU") {
				hasCookie = true
			}
			isAlive = cookieMgr.IsAlive()
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"has_cookie": hasCookie,
			"alive":      isAlive,
		})
	})
}
