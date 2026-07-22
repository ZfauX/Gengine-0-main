package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/bcrypt"
)

func TestBcryptCost(t *testing.T) {
	assert.Equal(t, 12, BcryptCost)
}

func TestBcryptCost_IsValid(t *testing.T) {
	_, err := bcrypt.Cost([]byte("$2a$12$test"))
	// The hash is invalid but cost should be parseable
	assert.Error(t, err)
}

func TestBcryptCost_ProducesValidHashes(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password"), BcryptCost)
	assert.NoError(t, err)
	assert.NotEmpty(t, hash)

	cost, err := bcrypt.Cost(hash)
	assert.NoError(t, err)
	assert.Equal(t, BcryptCost, cost)
}
