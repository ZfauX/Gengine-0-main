// internal/domain/social/repository.go
package social

import (
	"context"

	"gengine-0/internal/domain/user"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type FollowRepository interface {
	Follow(ctx context.Context, followerID, authorID uint) error
	Unfollow(ctx context.Context, followerID, authorID uint) error
	IsFollowing(ctx context.Context, followerID, authorID uint) (bool, error)
	GetSubscriptions(ctx context.Context, userID uint) ([]user.User, error)
	GetFollowers(ctx context.Context, authorID uint) ([]user.User, error)
}

type gormFollowRepo struct{ db *gorm.DB }

func NewGormFollowRepo(db *gorm.DB) FollowRepository {
	return &gormFollowRepo{db: db}
}

func (r *gormFollowRepo) Follow(ctx context.Context, followerID, authorID uint) error {
	follow := Follow{
		FollowerID: followerID,
		AuthorID:   authorID,
	}
	return r.db.WithContext(ctx).Create(&follow).Error
}

func (r *gormFollowRepo) Unfollow(ctx context.Context, followerID, authorID uint) error {
	result := r.db.WithContext(ctx).Unscoped().Where("follower_id = ? AND author_id = ?", followerID, authorID).Delete(&Follow{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		log.Warn().
			Uint("follower_id", followerID).
			Uint("author_id", authorID).
			Msg("Unfollow: no record found to delete")
	} else {
		log.Info().
			Uint("follower_id", followerID).
			Uint("author_id", authorID).
			Int64("rows_affected", result.RowsAffected).
			Msg("Unfollow: all records deleted successfully")
	}
	return nil
}

func (r *gormFollowRepo) IsFollowing(ctx context.Context, followerID, authorID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Follow{}).Where("follower_id = ? AND author_id = ?", followerID, authorID).Count(&count).Error
	return count > 0, err
}

func (r *gormFollowRepo) GetSubscriptions(ctx context.Context, userID uint) ([]user.User, error) {
	var authors []user.User
	err := r.db.WithContext(ctx).Joins("JOIN follows ON follows.author_id = users.id").
		Where("follows.follower_id = ?", userID).
		Find(&authors).Error
	return authors, err
}

func (r *gormFollowRepo) GetFollowers(ctx context.Context, authorID uint) ([]user.User, error) {
	var followers []user.User
	err := r.db.WithContext(ctx).Joins("JOIN follows ON follows.follower_id = users.id").
		Where("follows.author_id = ?", authorID).
		Find(&followers).Error
	return followers, err
}
