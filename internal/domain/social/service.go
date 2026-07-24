// internal/domain/social/service.go
package social

import (
	"context"
	"errors"

	"gengine-0/internal/domain/user"
)

var ErrNotFollowing = errors.New("не подписан")

// ---------- FollowService ----------

type FollowService struct {
	followRepo FollowRepository
}

func NewFollowService(followRepo FollowRepository) *FollowService {
	return &FollowService{followRepo: followRepo}
}

func (s *FollowService) Follow(ctx context.Context, followerID, authorID uint) error {
	if followerID == authorID {
		return errors.New("нельзя подписаться на самого себя")
	}
	exists, err := s.followRepo.IsFollowing(ctx, followerID, authorID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return s.followRepo.Follow(ctx, followerID, authorID)
}

func (s *FollowService) Unfollow(ctx context.Context, followerID, authorID uint) error {
	return s.followRepo.Unfollow(ctx, followerID, authorID)
}

func (s *FollowService) IsFollowing(ctx context.Context, followerID, authorID uint) bool {
	following, _ := s.followRepo.IsFollowing(ctx, followerID, authorID)
	return following
}

func (s *FollowService) GetSubscriptions(ctx context.Context, userID uint) ([]user.User, error) {
	return s.followRepo.GetSubscriptions(ctx, userID)
}

func (s *FollowService) GetFollowers(ctx context.Context, authorID uint) ([]user.User, error) {
	return s.followRepo.GetFollowers(ctx, authorID)
}
