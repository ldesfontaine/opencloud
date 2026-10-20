package ratelimit

import "testing"

func TestSourceKey(t *testing.T) {
	cases := []struct{ address, want string }{
		{"203.0.113.9", "203.0.113.9"},
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff:ffff:ffff:ffff", "2001:db8:1:2::/64"},
		{"2001:db8:1:3::1", "2001:db8:1:3::/64"},
		{"::ffff:203.0.113.9", "203.0.113.9"},
		{"fe80::1%eth0", "fe80::/64"},
		{"fe80::2%eth1", "fe80::/64"},
		{"::1", "::/64"},
		{"not-an-ip", "not-an-ip"},
		{"", ""},
	}
	for _, c := range cases {
		if got := sourceKey(c.address); got != c.want {
			t.Errorf("sourceKey(%q) = %q, want %q", c.address, got, c.want)
		}
	}
}

func TestSourceKey_ChangesOnlyWithTheSixtyFourBitPrefix(t *testing.T) {
	if sourceKey("2001:db8:1:2::1") != sourceKey("2001:db8:1:2::2") {
		t.Fatal("two addresses of one /64 must share a key")
	}
	if sourceKey("2001:db8:1:2::1") == sourceKey("2001:db8:1:3::1") {
		t.Fatal("two /64 must not share a key")
	}
}
