// Package storage предоставляет интерфейс и реализацию файлового хранилища.
package storage

import "io"

type FileStorage interface {
	Save(baseDir string, reader io.Reader, originalName string, userID uint, maxSize int64, allowedMIMETypes []string) (string, error)
	Delete(webPath string) error
}