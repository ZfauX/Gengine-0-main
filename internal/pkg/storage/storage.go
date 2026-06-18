package storage

import "io"

type FileStorage interface {
	Save(baseDir string, reader io.Reader, originalName string, userID uint, allowedMIMETypes []string) (string, error)
	Delete(webPath string) error
}