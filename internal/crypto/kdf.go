package crypto

import (
	"crypto/rand"

	"golang.org/x/crypto/argon2"
)

// Default Argon2id parameters.
const (
	DefaultArgonTime    uint32 = 3
	DefaultArgonMemKiB  uint32 = 64 * 1024
	DefaultArgonThreads uint8  = 4
	KeyLen              uint32 = 32
	SaltLen                    = 16
)

type ArgonParams struct {
	Time    uint32
	MemKiB  uint32
	Threads uint8
}

func DefaultArgonParams() ArgonParams {
	return ArgonParams{
		Time:    DefaultArgonTime,
		MemKiB:  DefaultArgonMemKiB,
		Threads: DefaultArgonThreads,
	}
}

// NewSalt returns a freshly randomized salt.
func NewSalt() ([]byte, error) {
	s := make([]byte, SaltLen)
	if _, err := rand.Read(s); err != nil {
		return nil, err
	}
	return s, nil
}

// DeriveKey derives a 32-byte key from a passphrase using Argon2id.
func DeriveKey(passphrase, salt []byte, p ArgonParams) []byte {
	return argon2.IDKey(passphrase, salt, p.Time, p.MemKiB, p.Threads, KeyLen)
}
