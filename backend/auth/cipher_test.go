package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenCipherBindsCiphertextToSessionAndTokenKind(t *testing.T) {
	var key [32]byte
	copy(key[:], "01234567890123456789012345678901")
	cipher := NewTokenCipher(key)

	encrypted, err := cipher.Encrypt("secret-token", "session-1:access")
	require.NoError(t, err)
	require.NotContains(t, encrypted, "secret-token")

	plaintext, err := cipher.Decrypt(encrypted, "session-1:access")
	require.NoError(t, err)
	require.Equal(t, "secret-token", plaintext)

	_, err = cipher.Decrypt(encrypted, "session-2:access")
	require.Error(t, err)
	_, err = cipher.Decrypt(encrypted, "session-1:refresh")
	require.Error(t, err)
}
