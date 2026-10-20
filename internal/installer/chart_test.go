// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"net/netip"
	"strings"
	"testing"
)

func TestLoadChart(t *testing.T) {
	chrt, err := LoadChart()
	if err != nil {
		t.Fatalf("LoadChart: %v", err)
	}
	if chrt.Metadata == nil || chrt.Metadata.Name != "rivora" {
		t.Errorf("chart name = %+v, want %q", chrt.Metadata, "rivora")
	}
	if len(chrt.Templates) == 0 {
		t.Error("chart has no templates — embed likely picked up the wrong directory")
	}
	if chrt.Values == nil {
		t.Error("chart has no default values.yaml")
	}
	// The AddressPool CRD is a chart-root crds/ file, not a template — make
	// sure the embed captured it too, since install() relies on Helm's own
	// CRD-install-once handling of chrt.CRDObjects().
	if len(chrt.CRDObjects()) == 0 {
		t.Error("chart has no CRDs — crds/addresspool-crd.yaml likely missing from the embed")
	}
}

// The CRD schema does not validate address syntax, so a typo in the CI sample
// (it once had "2001:db8:ci::/64", where "i" is not hex) would pass CI and be
// unparseable by the controller.
func TestCISampleAddressPoolPrefixesParse(t *testing.T) {
	chrt, err := LoadChart()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range chrt.Files {
		if f.Name != "ci/addresspool-ipv6.yaml" {
			continue
		}
		found = true
		n := 0
		for _, line := range strings.Split(string(f.Data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "- ") {
				continue
			}
			n++
			if _, err := netip.ParsePrefix(strings.TrimPrefix(line, "- ")); err != nil {
				t.Errorf("sample address %q: %v", line, err)
			}
		}
		if n == 0 {
			t.Error("sample has no addresses")
		}
	}
	if !found {
		t.Skip("ci/addresspool-ipv6.yaml not in the loaded chart files")
	}
}
