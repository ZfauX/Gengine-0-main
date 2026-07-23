// Package tools — вспомогательный файл для фиксации tool-зависимостей в go.mod.
// См. https://github.com/golang/go/wiki/Modules#how-can-i-track-tool-dependencies-for-a-module
//go:build tools
// +build tools

package tools

import (
	_ "go.uber.org/mock/mockgen"
)
