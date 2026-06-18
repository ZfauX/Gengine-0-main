// internal/domain/social/service.go
package social

import (
	"errors"

	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

// ---------- FollowService ----------

type FollowService struct {
	DB *gorm.DB
}

func NewFollowService(db *gorm.DB) *FollowService {
	return &FollowService{DB: db}
}

func (s *FollowService) Follow(followerID, authorID uint) error {
	if followerID == authorID {
		return errors.New("нельзя подписаться на самого себя")
	}
	var count int64
	s.DB.Model(&Follow{}).Where("follower_id = ? AND author_id = ?", followerID, authorID).Count(&count)
	if count > 0 {
		return nil
	}
	return s.DB.Create(&Follow{FollowerID: followerID, AuthorID: authorID}).Error
}

func (s *FollowService) Unfollow(followerID, authorID uint) error {
	return s.DB.Where("follower_id = ? AND author_id = ?", followerID, authorID).Delete(&Follow{}).Error
}

func (s *FollowService) IsFollowing(followerID, authorID uint) bool {
	var count int64
	s.DB.Model(&Follow{}).Where("follower_id = ? AND author_id = ?", followerID, authorID).Count(&count)
	return count > 0
}

func (s *FollowService) GetSubscriptions(userID uint) ([]user.User, error) {
	var authors []user.User
	err := s.DB.Joins("JOIN follows ON follows.author_id = users.id").
		Where("follows.follower_id = ?", userID).
		Find(&authors).Error
	return authors, err
}

func (s *FollowService) GetFollowers(authorID uint) ([]user.User, error) {
	var followers []user.User
	err := s.DB.Joins("JOIN follows ON follows.follower_id = users.id").
		Where("follows.author_id = ?", authorID).
		Find(&followers).Error
	return followers, err
}