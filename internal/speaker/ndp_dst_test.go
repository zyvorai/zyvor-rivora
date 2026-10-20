// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0
package speaker

import (
	"net/netip"
	"testing"
)

func TestNDPReplyDst(t *testing.T) {
	ll := netip.MustParseAddr("fe80::1")
	if got := ndpReplyDst(ll); got != ll {
		t.Errorf("unicast solicitation: dst = %v, want %v", got, ll)
	}
	if got := ndpReplyDst(netip.IPv6Unspecified()); got != netip.IPv6LinkLocalAllNodes() {
		t.Errorf("DAD solicitation from ::: dst = %v, want all-nodes", got)
	}
}
