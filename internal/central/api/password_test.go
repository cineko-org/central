package api

import (
	"errors"
	"strings"
	"testing"
)

func TestPasswordHasherRoundTrip(t *testing.T) {
	hasher, err := newPasswordHasher(adminTestPepper)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash(adminTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("password hash = %q", encoded)
	}
	verified, err := hasher.Verify(adminTestPassword, encoded)
	if err != nil || !verified {
		t.Fatalf("Verify(correct) = %v, %v", verified, err)
	}
	verified, err = hasher.Verify("wrong-password", encoded)
	if err != nil || verified {
		t.Fatalf("Verify(wrong) = %v, %v", verified, err)
	}
	other, err := newPasswordHasher("abcdef0123456789abcdef0123456789")
	if err != nil {
		t.Fatal(err)
	}
	verified, err = other.Verify(adminTestPassword, encoded)
	if err != nil || verified {
		t.Fatalf("Verify(other pepper) = %v, %v", verified, err)
	}
}

func TestPasswordHasherRejectsInvalidConfiguration(t *testing.T) {
	if _, err := newPasswordHasher("short"); err == nil {
		t.Fatal("short pepper accepted")
	}
	hasher, err := newPasswordHasher(adminTestPepper)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{
		"",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$MDEyMzQ1Njc4OWFiY2RlZg",
		"$argon2id$v=16$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$MDEyMzQ1Njc4OWFiY2RlZg",
		"$argon2id$v=19$invalid$c2FsdHNhbHRzYWx0c2FsdA$MDEyMzQ1Njc4OWFiY2RlZg",
		"$argon2id$v=19$m=1024,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$MDEyMzQ1Njc4OWFiY2RlZg",
		"$argon2id$v=19$m=65536,t=3,p=2$bad$MDEyMzQ1Njc4OWFiY2RlZg",
		"$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$bad",
	} {
		if verified, err := hasher.Verify("password", encoded); err == nil || verified {
			t.Fatalf("Verify(%q) = %v, %v", encoded, verified, err)
		}
	}
}

func TestPasswordHasherPropagatesSaltFailures(t *testing.T) {
	hasher, err := newPasswordHasher(adminTestPepper)
	if err != nil {
		t.Fatal(err)
	}
	hasher.random = func([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
	if _, err := hasher.Hash(adminTestPassword); err == nil {
		t.Fatal("entropy failure accepted")
	}
	hasher.random = func(buffer []byte) (int, error) { return len(buffer) - 1, nil }
	if _, err := hasher.Hash(adminTestPassword); err == nil {
		t.Fatal("short entropy read accepted")
	}
}
