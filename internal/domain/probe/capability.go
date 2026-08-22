package probe

import (
	"fmt"
	"sort"

	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
)

const (
	CapabilityCGVScheduleCapture         = "cgv.schedule.capture"
	CapabilityCGVCatalogCapture          = "cgv.catalog.capture"
	CapabilityCGVSeatMapCapture          = "cgv.seat-map.capture"
	CapabilityCGVSeatAvailabilityCapture = "cgv.seat-availability.capture"
)

// IsSupportedCapability reports whether Central can assign the capability to a probe.
func IsSupportedCapability(value string) bool {
	switch value {
	case CapabilityCGVScheduleCapture, CapabilityCGVCatalogCapture, CapabilityCGVSeatMapCapture,
		CapabilityCGVSeatAvailabilityCapture:
		return true
	default:
		return false
	}
}

// CapabilityKey returns the stable database identity for a typed capability.
func CapabilityKey(capability *observationpb.Capability) (string, error) {
	switch {
	case capability == nil:
		return "", fmt.Errorf("capability is required")
	case capability.GetScheduleCapture() != nil:
		return CapabilityCGVScheduleCapture, nil
	case capability.GetCatalogCapture() != nil:
		return CapabilityCGVCatalogCapture, nil
	case capability.GetSeatMapCapture() != nil:
		return CapabilityCGVSeatMapCapture, nil
	case capability.GetSeatAvailabilityCapture() != nil:
		return CapabilityCGVSeatAvailabilityCapture, nil
	default:
		return "", fmt.Errorf("unsupported capability")
	}
}

// CapabilityKeys converts typed capabilities only at the persistence boundary.
func CapabilityKeys(capabilities []*observationpb.Capability) ([]string, error) {
	keys := make([]string, 0, len(capabilities))
	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		key, err := CapabilityKey(capability)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("duplicate capability %q", key)
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

// Capabilities converts persisted capability identities back into Proto values.
func Capabilities(keys []string) ([]*observationpb.Capability, error) {
	capabilities := make([]*observationpb.Capability, 0, len(keys))
	for _, key := range keys {
		capability := &observationpb.Capability{}
		switch key {
		case CapabilityCGVScheduleCapture:
			capability.SetScheduleCapture(&observationpb.ScheduleCapture{})
		case CapabilityCGVCatalogCapture:
			capability.SetCatalogCapture(&observationpb.CatalogCapture{})
		case CapabilityCGVSeatMapCapture:
			capability.SetSeatMapCapture(&observationpb.SeatMapCapture{})
		case CapabilityCGVSeatAvailabilityCapture:
			capability.SetSeatAvailabilityCapture(&observationpb.SeatAvailabilityCapture{})
		default:
			return nil, fmt.Errorf("unsupported capability %q", key)
		}
		capabilities = append(capabilities, capability)
	}
	return capabilities, nil
}
