// internal/domain/admin/model.go
package admin

import (
	"context"
	"time"

	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

// AuditLog — запись в журнале аудита действий пользователей.
type AuditLog struct {
	gorm.Model
	UserID     uint      `gorm:"not null;index"`
	User       user.User `gorm:"foreignKey:UserID"`
	Action     string    `gorm:"not null"`
	ObjectType string    `gorm:"not null"`
	ObjectID   uint      `gorm:"not null"`
	Details    string    `gorm:"type:text"`
}

// Backup — информация о резервной копии базы данных.
type Backup struct {
	ID        uint   `gorm:"primaryKey"`
	Filename  string `gorm:"not null"`
	FilePath  string `gorm:"not null"`
	Size      int64
	CreatedAt time.Time
}

// ---------- Repository interfaces ----------

// BackupRepository определяет контракт для работы с резервными копиями.
type BackupRepository interface {
	Create(ctx context.Context, backup *Backup) error
	GetByID(ctx context.Context, id uint) (*Backup, error)
	List(ctx context.Context) ([]Backup, error)
	Delete(ctx context.Context, id uint) error
	Count(ctx context.Context) (int64, error)
}

// ---------- GORM implementation ----------

type gormBackupRepo struct{ db *gorm.DB }

func NewGormBackupRepo(db *gorm.DB) BackupRepository {
	return &gormBackupRepo{db: db}
}

func (r *gormBackupRepo) Create(ctx context.Context, backup *Backup) error {
	return r.db.WithContext(ctx).Create(backup).Error
}

func (r *gormBackupRepo) GetByID(ctx context.Context, id uint) (*Backup, error) {
	var b Backup
	err := r.db.WithContext(ctx).First(&b, id).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *gormBackupRepo) List(ctx context.Context) ([]Backup, error) {
	var backups []Backup
	err := r.db.WithContext(ctx).Order("created_at DESC").Find(&backups).Error
	return backups, err
}

func (r *gormBackupRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Backup{}, id).Error
}

func (r *gormBackupRepo) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Backup{}).Count(&count).Error
	return count, err
}
