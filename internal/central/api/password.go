package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLength  = 16
	argonKeyLength   = 32
)

type passwordHasher struct {
	pepper []byte
	random func([]byte) (int, error)
}

type parsedPasswordHash struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	digest      []byte
}

func newPasswordHasher(pepper string) (*passwordHasher, error) {
	if len(pepper) < 32 {
		return nil, errors.New("CINEKO_ADMIN_PASSWORD_PEPPER must be at least 32 characters")
	}
	return &passwordHasher{pepper: []byte(pepper), random: rand.Read}, nil
}

func (hasher *passwordHasher) Hash(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if count, err := hasher.random(salt); err != nil || count != len(salt) {
		if err == nil {
			err = errors.New("generate complete password salt")
		}
		return "", err
	}
	digest := hasher.derive(password, salt, argonMemory, argonIterations, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(digest)), nil
}

func (hasher *passwordHasher) Verify(password, encoded string) (bool, error) {
	parsed, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	// The parser rejects digests outside 16..64 bytes before this conversion.
	keyLength := uint32(len(parsed.digest)) // #nosec G115 -- bounded by parsePasswordHash
	actual := hasher.derive(
		password,
		parsed.salt,
		parsed.memory,
		parsed.iterations,
		parsed.parallelism,
		keyLength,
	)
	return subtle.ConstantTimeCompare(actual, parsed.digest) == 1, nil
}

func parsePasswordHash(encoded string) (parsedPasswordHash, error) {
	parts := strings.Split(encoded, "$")
	if !validPasswordHashHeader(parts) {
		return parsedPasswordHash{}, errors.New("invalid Argon2id password hash")
	}
	memory, iterations, parallelism, err := parseArgonParameters(parts[3])
	if err != nil {
		return parsedPasswordHash{}, err
	}
	salt, err := decodeArgonComponent(parts[4], 16, 64, "salt")
	if err != nil {
		return parsedPasswordHash{}, err
	}
	digest, err := decodeArgonComponent(parts[5], 16, 64, "digest")
	if err != nil {
		return parsedPasswordHash{}, err
	}
	return parsedPasswordHash{
		memory: memory, iterations: iterations, parallelism: parallelism,
		salt: salt, digest: digest,
	}, nil
}

func validPasswordHashHeader(parts []string) bool {
	return len(parts) == 6 && parts[1] == "argon2id" && parts[2] == "v="+strconv.Itoa(argon2.Version)
}

func parseArgonParameters(value string) (uint32, uint32, uint8, error) {
	parameters := strings.Split(value, ",")
	if len(parameters) != 3 {
		return 0, 0, 0, errors.New("invalid Argon2id parameters")
	}
	memory, memoryErr := parseArgonParameter(parameters[0], "m=", 32)
	iterations, iterationsErr := parseArgonParameter(parameters[1], "t=", 32)
	parallelism, parallelismErr := parseArgonParameter(parameters[2], "p=", 8)
	if memoryErr != nil || iterationsErr != nil || parallelismErr != nil {
		return 0, 0, 0, errors.New("invalid Argon2id parameters")
	}
	memoryValue, iterationsValue := uint32(memory), uint32(iterations) // #nosec G115 -- ParseUint bit sizes bound both values
	parallelismValue := uint8(parallelism)                             // #nosec G115 -- ParseUint bit size bounds the value
	if memoryValue < 8*1024 || memoryValue > 128*1024 || iterationsValue < 1 || iterationsValue > 10 || parallelismValue < 1 || parallelismValue > 8 {
		return 0, 0, 0, errors.New("Argon2id parameters outside allowed range")
	}
	return memoryValue, iterationsValue, parallelismValue, nil
}

func decodeArgonComponent(value string, minimum, maximum int, name string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil || len(decoded) < minimum || len(decoded) > maximum {
		return nil, fmt.Errorf("invalid Argon2id %s", name)
	}
	return decoded, nil
}

func parseArgonParameter(value, prefix string, bitSize int) (uint64, error) {
	if !strings.HasPrefix(value, prefix) {
		return 0, errors.New("invalid Argon2id parameter name")
	}
	return strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, bitSize)
}

func (hasher *passwordHasher) derive(
	password string,
	salt []byte,
	memory uint32,
	iterations uint32,
	parallelism uint8,
	keyLength uint32,
) []byte {
	mac := hmac.New(sha256.New, hasher.pepper)
	_, _ = mac.Write([]byte(password))
	return argon2.IDKey(mac.Sum(nil), salt, iterations, memory, parallelism, keyLength)
}
