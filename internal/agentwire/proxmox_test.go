package agentwire

import (
	"testing"
	"time"
)

func TestSnapshotName_IsTheOnlyShapeIsOwnSnapshotAccepts(t *testing.T) {
	name := SnapshotName(time.Date(2026, 10, 28, 9, 5, 3, 0, time.FixedZone("x", 3600)))
	if name != "oc-20261028-080503" || !IsOwnSnapshot(name) {
		t.Fatalf("name %q", name)
	}
	for _, other := range []string{"", "oc-", "oc-manuel", "avant", "oc-20261028-08050", "oc-20261028-0805033", "xoc-20261028-080503", "oc-20261028-080503\n", "oc-20261028-080503/../x"} {
		if IsOwnSnapshot(other) {
			t.Errorf("%q passed", other)
		}
	}
}

func TestProxmoxGuestParams_ValidateIsStrictPerKind(t *testing.T) {
	good := ProxmoxGuestParams{VMID: 105, Type: GuestQEMU}
	for _, kind := range []Kind{KindProxmoxStart, KindProxmoxShutdown, KindProxmoxSnapshot} {
		if err := good.Validate(kind); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
	}
	if err := (ProxmoxGuestParams{VMID: 105, Type: GuestLXC, Snapshot: "oc-20261028-080503"}).Validate(KindProxmoxSnapshotDelete); err != nil {
		t.Error(err)
	}
	bad := []struct {
		params ProxmoxGuestParams
		kind   Kind
	}{
		{ProxmoxGuestParams{VMID: 99, Type: GuestQEMU}, KindProxmoxStart},
		{ProxmoxGuestParams{VMID: 1000000000, Type: GuestQEMU}, KindProxmoxStart},
		{ProxmoxGuestParams{VMID: 105, Type: "kvm"}, KindProxmoxStart},
		{ProxmoxGuestParams{VMID: 105, Type: GuestQEMU, Snapshot: "oc-20261028-080503"}, KindProxmoxSnapshot},
		{good, KindProxmoxSnapshotDelete},
	}
	for _, c := range bad {
		if c.params.Validate(c.kind) == nil {
			t.Errorf("%+v for %s passed", c.params, c.kind)
		}
	}
	if !IsSnapshotNotOurs((ProxmoxGuestParams{VMID: 105, Type: GuestQEMU, Snapshot: "x"}).Validate(KindProxmoxSnapshotDelete)) {
		t.Error("a foreign snapshot is its own refusal")
	}
}

func TestGuestOrders_AreOrdersNotObservations(t *testing.T) {
	for _, kind := range []Kind{KindProxmoxStart, KindProxmoxShutdown, KindProxmoxSnapshot, KindProxmoxSnapshotDelete} {
		if !kind.IsOrder() || !kind.IsGuestOrder() {
			t.Errorf("%s", kind)
		}
	}
}
