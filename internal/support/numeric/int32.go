// Package numeric provides explicit, bounded conversions shared across domains.
package numeric

const (
	maximumInt32 = int64(1<<31 - 1)
	minimumInt32 = int64(-1 << 31)
)

// ClampInt32 converts an integer without allowing platform-width overflow.
func ClampInt32(value int) int32 {
	return ClampInt64ToInt32(int64(value))
}

// ClampInt64ToInt32 converts an int64 and saturates values outside the Proto field range.
func ClampInt64ToInt32(value int64) int32 {
	switch {
	case value > maximumInt32:
		return int32(maximumInt32)
	case value < minimumInt32:
		return int32(minimumInt32)
	default:
		return int32(value) // #nosec G115 -- the switch above proves the conversion is in range.
	}
}
