package catalog

import "time"

// SeatMapCollectionRetryLimit is the number of automatic retries allowed
// after the initial attempt. Failures 1 through 5 schedule another attempt
// with 15s, 30s, 1m, 2m, and 4m delays; failure 6 is blocked.
const SeatMapCollectionRetryLimit = 5

// SeatMapRetryDelay returns the Central-owned exponential delay for the next
// attempt. Values beyond the retry limit stay at the final 4m delay; the
// caller must stop scheduling once SeatMapCollectionBlockedAfter is true.
func SeatMapRetryDelay(consecutiveFailures int) time.Duration {
	if consecutiveFailures < 1 {
		consecutiveFailures = 1
	}
	if consecutiveFailures > SeatMapCollectionRetryLimit {
		consecutiveFailures = SeatMapCollectionRetryLimit
	}
	delay := 15 * time.Second
	for index := 1; index < consecutiveFailures; index++ {
		delay *= 2
	}
	return delay
}

// SeatMapCollectionBlockedAfter reports whether a failure has exhausted all
// automatic retries and must wait for a new objective trigger. The sixth
// failure is the first blocked outcome.
func SeatMapCollectionBlockedAfter(consecutiveFailures int) bool {
	return consecutiveFailures > SeatMapCollectionRetryLimit
}
