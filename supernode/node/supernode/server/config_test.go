package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewConfig_Defaults(t *testing.T) {
	cfg := NewConfig()

	assert.NotNil(t, cfg)
	assert.Equal(t, "", cfg.ListenAddresses, "default listen address should be empty")
	assert.Equal(t, 4444, cfg.Port, "default port should be 4444")
	assert.Equal(t, "", cfg.Identity, "default identity should be empty")
}
