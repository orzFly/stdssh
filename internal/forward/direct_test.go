package forward

import (
	"net"
	"testing"
)

func mustParseCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDestAllowedEmptyPermitsAll(t *testing.T) {
	if !destAllowed("192.168.1.1", nil, nil) {
		t.Error("nil allow+deny should permit any destination")
	}
	if !destAllowed("10.0.0.1", []*net.IPNet{}, nil) {
		t.Error("empty allow should permit any destination")
	}
}

func TestDestAllowedIPMatch(t *testing.T) {
	allow := []*net.IPNet{mustParseCIDR(t, "10.0.0.0/8")}
	if !destAllowed("10.1.2.3", allow, nil) {
		t.Error("10.1.2.3 should match 10.0.0.0/8")
	}
	if destAllowed("192.168.1.1", allow, nil) {
		t.Error("192.168.1.1 should not match 10.0.0.0/8")
	}
}

func TestDestAllowedMultipleCIDRs(t *testing.T) {
	allow := []*net.IPNet{
		mustParseCIDR(t, "10.0.0.0/8"),
		mustParseCIDR(t, "172.16.0.0/12"),
	}
	if !destAllowed("10.1.2.3", allow, nil) {
		t.Error("should match first CIDR")
	}
	if !destAllowed("172.20.0.1", allow, nil) {
		t.Error("should match second CIDR")
	}
	if destAllowed("8.8.8.8", allow, nil) {
		t.Error("should not match any CIDR")
	}
}

func TestDestAllowedLoopback(t *testing.T) {
	allow := []*net.IPNet{mustParseCIDR(t, "127.0.0.0/8")}
	if !destAllowed("127.0.0.1", allow, nil) {
		t.Error("127.0.0.1 should match 127.0.0.0/8")
	}
	if destAllowed("10.0.0.1", allow, nil) {
		t.Error("10.0.0.1 should not match 127.0.0.0/8")
	}
}

func TestDestAllowedIPv6(t *testing.T) {
	allow := []*net.IPNet{mustParseCIDR(t, "fd00::/8")}
	if !destAllowed("fd12::1", allow, nil) {
		t.Error("fd12::1 should match fd00::/8")
	}
	if destAllowed("2001:db8::1", allow, nil) {
		t.Error("2001:db8::1 should not match fd00::/8")
	}
}

func TestDestDenyTakesPrecedence(t *testing.T) {
	allow := []*net.IPNet{mustParseCIDR(t, "10.0.0.0/8")}
	deny := []*net.IPNet{mustParseCIDR(t, "10.0.0.0/24")}
	if !destAllowed("10.1.0.1", allow, deny) {
		t.Error("10.1.0.1 should be allowed (in allow, not in deny)")
	}
	if destAllowed("10.0.0.5", allow, deny) {
		t.Error("10.0.0.5 should be denied (deny takes precedence)")
	}
}

func TestDestDenyOnlyBlocksSpecific(t *testing.T) {
	deny := []*net.IPNet{mustParseCIDR(t, "169.254.169.254/32")}
	if !destAllowed("10.0.0.1", nil, deny) {
		t.Error("10.0.0.1 should be allowed (not in deny, no allow restriction)")
	}
	if destAllowed("169.254.169.254", nil, deny) {
		t.Error("metadata IP should be denied")
	}
}

func TestDestDenyIPv6(t *testing.T) {
	deny := []*net.IPNet{mustParseCIDR(t, "fe80::/10")}
	if destAllowed("fe80::1", nil, deny) {
		t.Error("link-local IPv6 should be denied")
	}
	if !destAllowed("fd00::1", nil, deny) {
		t.Error("ULA IPv6 should be allowed")
	}
}
