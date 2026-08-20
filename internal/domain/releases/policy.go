package releases

import (
	"math/big"
	"strings"
)

// CanonicalVersion adds the semantic-version prefix used by release policy.
func CanonicalVersion(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "v") {
		return value
	}
	return "v" + value
}

// IsNumericRevision reports whether a browser revision contains only decimal
// digits. Surrounding whitespace is accepted because release inputs are
// normalized before validation.
func IsNumericRevision(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

// CompareNumericRevision compares two validated decimal browser revisions.
func CompareNumericRevision(left, right string) int {
	leftValue, _ := new(big.Int).SetString(strings.TrimSpace(left), 10)
	rightValue, _ := new(big.Int).SetString(strings.TrimSpace(right), 10)
	return leftValue.Cmp(rightValue)
}
