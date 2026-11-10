package repo

import (
	"testing"

	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// The migration and the Go seed must stay in sync: every managed capability
// gets one seeded point, IDs stay unique, and tenant-allocated IDs begin
// above the SmartAdmin hardcoded range.
func TestSeedCatalogConsistency(t *testing.T) {
	managed := make(map[authz.Capability]bool)
	for _, capability := range authz.ManagedCapabilities() {
		managed[capability] = true
	}
	seenIDs := make(map[uint64]bool)
	points := 0
	buttons := 0
	for _, menu := range seedMenus {
		if seenIDs[menu.id] {
			t.Fatalf("duplicate seeded menu id %d", menu.id)
		}
		seenIDs[menu.id] = true
		if menu.id >= customMenuIDStart {
			t.Fatalf("seeded menu id %d inside custom range", menu.id)
		}
		if menu.typ != 3 {
			continue
		}
		if menu.webPerms != "" {
			if menu.capability != "" {
				t.Fatalf("operator button %d cannot carry a capability", menu.id)
			}
			buttons++
			if !operatorButtonIDs[menu.id] {
				t.Fatalf("button id %d missing from operatorButtonIDs", menu.id)
			}
			continue
		}
		points++
		if !managed[authz.Capability(menu.capability)] {
			t.Fatalf("seeded point %d carries unmanaged capability %q", menu.id, menu.capability)
		}
	}
	if points != len(managed) {
		t.Fatalf("seeded capability points = %d, managed capabilities = %d", points, len(managed))
	}
	if points == 0 || buttons != 67 {
		t.Fatalf("seed counts points=%d buttons=%d", points, buttons)
	}
	if len(seedRoles) != 3 {
		t.Fatalf("seed roles = %d, want 3", len(seedRoles))
	}
}
