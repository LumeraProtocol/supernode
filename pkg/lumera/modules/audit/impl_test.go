package audit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewModuleRejectsNilConnection(t *testing.T) {
	m, err := NewModule(nil)
	require.Nil(t, m)
	require.ErrorContains(t, err, "connection cannot be nil")
}
