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

// Minimum Argon2id parameters. Configs whose [_meta] specifies values
// below these floors are rejected at derive time (security #111): the
// params are stored unauthenticated on disk, so without floors a
// config-write attacker can silently weaken the KDF and reduce the
// cost of offline brute-force against a leaked memory/swap dump.
//
// MinArgonMemKiB tracks the OWASP Password Storage cheat sheet's
// Argon2id minimum of 19 MiB; the rest are conservative floors well
// below the project defaults so legitimately-strict (but slower)
// configurations remain valid.
const (
	MinArgonTime    uint32 = 2
	MinArgonMemKiB  uint32 = 19 * 1024
	MinArgonThreads uint8  = 1
	MinSaltLen             = 16
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
