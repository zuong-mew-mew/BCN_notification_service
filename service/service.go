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

func IdentifyUser(zaloUID string, fullName string, mentorUIDList []string) User {
	role := RoleUser
	for _, mUID := range mentorUIDList {
		if zaloUID == mUID {
			role = RoleMentor
			break
		}
	}
	return User{
		ZaloUID:  zaloUID,
		FullName: fullName,
		Role:     role,
	}
}

func CanApprove(user User) bool {
	return user.Role == RoleMentor
}

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

func ApprovePost(mentor User, post *Post) error {
	if !CanApprove(mentor) {
		return errors.New("unauthorized: approval requires mentor role")
	}
	post.Status = StatusApproved
	return nil
}

func RejectPost(mentor User, post *Post) error {
	if !CanApprove(mentor) {
		return errors.New("unauthorized: rejection requires mentor role")
	}
	post.Status = StatusRejected
	return nil
}

func IsReadyToPublish(post Post) bool {
	if post.Status != StatusApproved {
		return false
	}
	if post.PublishAt == nil || !time.Now().Before(*post.PublishAt) {
		return true
	}
	return false
}
