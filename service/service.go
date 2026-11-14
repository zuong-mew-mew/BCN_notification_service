package service

import (
	"errors"
	"time"
)

const (
	RoleUser       = "user"
	RoleMentor     = "mentor"
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
)

type User struct {
	ZaloUID  string
	FullName string
	Role     string
}

type Post struct {
	ID             string
	Title          string
	Content        string
	TargetChannels []string
	CreatedBy      string
	Status         string
	PublishAt      *time.Time
}

// Hàm check user có phải là mentor hay không bằng zaloUID
func IdentifyUser(zaloUID string, fUllName string, mentorUIDlist []string) User {
	role := RoleUser

	for _, mentorUID := range mentorUIDlist {
		if zaloUID == mentorUID {
			role = RoleMentor
			break
		}
	}
	return User{
		ZaloUID:  zaloUID,
		FullName: fUllName,
		Role:     role,
	}
}

// Hàm kiểm tra User có quyền duyệt bài hay không
func CanApprove(user User) bool {
	return user.Role == RoleMentor
}

// Tạo bài viết mới
func CreatePost(author User, title string, content string, targetChannels []string, publishAt *time.Time) Post {
	return Post{
		ID:             "post_01",
		Title:          title,
		Content:        content,
		TargetChannels: targetChannels,
		CreatedBy:      author.ZaloUID,
		Status:         StatusPending,
		PublishAt:      publishAt,
	}
}

// Mentor duyệt bài
func ApprovePost(mentor User, post *Post) error {
	if CanApprove(mentor) == true {
		post.Status = StatusApproved
		return nil
	} else {
		return errors.New("bạn không có quyền duyệt bài")
	}
}

// mentor không duyệt bài
func RejectPost(mentor User, post *Post) error {
	if CanApprove(mentor) == true {
		post.Status = StatusRejected
		return nil
	} else {
		return errors.New("User không có quyền từ chối bài đăng ")
	}
}

// Kiểm tra bài viết đã đủ điều kiện để phát sóng hay chưa
func IsReadyToPublish(post Post) bool {
	if post.Status != StatusApproved {
		return false
	}

	if post.PublishAt == nil || time.Now().After(*post.PublishAt) || time.Now().Equal(*post.PublishAt) {
		return true
	}

	return false
}
