package service

import (
	"errors"
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
func CreatePost(author User, title string, content string, targetChannels []string) Post {
	return Post{
		ID:             "post_01",
		Title:          title,
		Content:        content,
		TargetChannels: targetChannels,
		CreatedBy:      author.ZaloUID,
		Status:         StatusPending,
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

func RejectPost(mentor User, post *Post) error {
	if CanApprove(mentor) == true {
		post.Status = StatusRejected
		return nil
	} else {
		return errors.New("User không có quyền từ chối bài đăng ")
	}
}
