package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const ProviderCGV = "cgv"

// CatalogID derives a stable provider-scoped identity from the upstream key.
func CatalogID(providerID, kind, sourceKey string) string {
	normalized := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(providerID), strings.TrimSpace(kind), strings.TrimSpace(sourceKey),
	}, "\x00"))
	digest := sha256.Sum256([]byte(normalized))
	return strings.TrimSpace(kind) + "_" + hex.EncodeToString(digest[:16])
}

// SeatMapVersionID derives the immutable identity of a normalized layout.
func SeatMapVersionID(auditoriumID, layoutHash string) string {
	return CatalogID("catalog", "seat-map", strings.TrimSpace(auditoriumID)+"\x00"+strings.TrimSpace(layoutHash))
}

// SeatID derives the stable identity of a labeled seat in an auditorium.
func SeatID(auditoriumID, label string) string {
	return CatalogID("catalog", "seat", strings.TrimSpace(auditoriumID)+"\x00"+strings.ToUpper(strings.TrimSpace(label)))
}
