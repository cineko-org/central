package api

import (
	"crypto/sha256"
	"sync"
	"time"

	"github.com/cineko-org/central/internal/central"
)

const (
	defaultAdminLoginConcurrency = 4
	adminLoginFailureLimit       = 10
	adminLoginBlockTime          = 10 * time.Minute
	adminLoginAttemptRetention   = 24 * time.Hour
	adminLoginAttemptCapacity    = 4096
)

type adminLoginAttempt struct {
	failures     int
	blockedUntil time.Time
	updatedAt    time.Time
}

type adminLoginLimiter struct {
	mu       sync.Mutex
	attempts map[[32]byte]adminLoginAttempt
	slots    chan struct{}
}

func newAdminLoginLimiter(maxConcurrency int) *adminLoginLimiter {
	if maxConcurrency <= 0 {
		maxConcurrency = defaultAdminLoginConcurrency
	}
	return &adminLoginLimiter{
		attempts: make(map[[32]byte]adminLoginAttempt),
		slots:    make(chan struct{}, maxConcurrency),
	}
}

func (limiter *adminLoginLimiter) acquire(source, userID string, now time.Time) (func(), error) {
	keys := adminLoginAttemptKeys(source, userID)
	limiter.mu.Lock()
	for key, attempt := range limiter.attempts {
		if now.Sub(attempt.updatedAt) > adminLoginAttemptRetention && !attempt.blockedUntil.After(now) {
			delete(limiter.attempts, key)
		}
	}
	for _, key := range keys {
		if limiter.attempts[key].blockedUntil.After(now) {
			limiter.mu.Unlock()
			return nil, central.ErrRateLimited
		}
	}
	limiter.mu.Unlock()

	select {
	case limiter.slots <- struct{}{}:
		return func() { <-limiter.slots }, nil
	default:
		return nil, central.ErrRateLimited
	}
}

func (limiter *adminLoginLimiter) recordFailure(source, userID string, knownUser bool, now time.Time) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limited := false
	keys := adminLoginAttemptKeys(source, userID)
	if !knownUser {
		keys = keys[:1]
	}
	for _, key := range keys {
		if _, exists := limiter.attempts[key]; !exists {
			limiter.makeAttemptRoom()
		}
		attempt := limiter.attempts[key]
		if !attempt.blockedUntil.IsZero() && !attempt.blockedUntil.After(now) {
			attempt.failures = 0
		}
		attempt.failures++
		attempt.updatedAt = now
		if attempt.failures >= adminLoginFailureLimit {
			attempt.blockedUntil = now.Add(adminLoginBlockTime)
			limited = true
		}
		limiter.attempts[key] = attempt
	}
	return limited
}

func (limiter *adminLoginLimiter) makeAttemptRoom() {
	if len(limiter.attempts) < adminLoginAttemptCapacity {
		return
	}
	var oldestKey [32]byte
	oldestAt := time.Time{}
	for key, attempt := range limiter.attempts {
		if oldestAt.IsZero() || attempt.updatedAt.Before(oldestAt) {
			oldestKey, oldestAt = key, attempt.updatedAt
		}
	}
	delete(limiter.attempts, oldestKey)
}

func adminLoginAttemptKeys(source, userID string) [][32]byte {
	return [][32]byte{
		sha256.Sum256([]byte("source\x00" + source)),
		sha256.Sum256([]byte("user\x00" + userID)),
	}
}
