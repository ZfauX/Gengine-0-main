package util

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatDuration_Hours(t *testing.T) {
	assert.Equal(t, "1ч 30м 00с", FormatDuration(90*time.Minute))
}

func TestFormatDuration_Minutes(t *testing.T) {
	assert.Equal(t, "5м 30с", FormatDuration(5*time.Minute+30*time.Second))
}

func TestFormatDuration_Seconds(t *testing.T) {
	assert.Equal(t, "0м 45с", FormatDuration(45*time.Second))
}

func TestFormatDuration_Zero(t *testing.T) {
	assert.Equal(t, "0м 00с", FormatDuration(0))
}

func TestFormatDuration_ExactHour(t *testing.T) {
	assert.Equal(t, "2ч 00м 00с", FormatDuration(2*time.Hour))
}

func TestFormatDuration_WithMinutes(t *testing.T) {
	assert.Equal(t, "3ч 15м 45с", FormatDuration(3*time.Hour+15*time.Minute+45*time.Second))
}
