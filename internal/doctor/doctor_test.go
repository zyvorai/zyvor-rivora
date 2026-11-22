// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0
package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseKernelVersion(t *testing.T) {
	cases := []struct {
		release      string
		major, minor int
		ok           bool
	}{
		{"6.8.0-139-generic", 6, 8, true},
		{"5.10.0-30-amd64", 5, 10, true},
		{"6.6.0", 6, 6, true},
		{"bogus", 0, 0, false},
	}
	for _, c := range cases {
		major, minor, ok := parseKernelVersion(c.release)
		if ok != c.ok || (ok && (major != c.major || minor != c.minor)) {
			t.Errorf("parseKernelVersion(%q) = (%d, %d, %v), want (%d, %d, %v)",
				c.release, major, minor, ok, c.major, c.minor, c.ok)
		}
	}
}

func TestCheckKernelVersionTCX(t *testing.T) {
	if c := checkKernelVersion("6.8.0-139-generic", false); c.Status != StatusPass {
		t.Errorf("6.8 kernel: got %s, want pass", c.Status)
	}
	if c := checkKernelVersion("5.15.0-1-generic", false); c.Status != StatusWarn {
		t.Errorf("5.15 without --require-tcx: got %s, want warn", c.Status)
	}
	if c := checkKernelVersion("5.15.0-1-generic", true); c.Status != StatusFail {
		t.Errorf("5.15 with --require-tcx: got %s, want fail", c.Status)
	}
}

func TestParseEthtoolDriver(t *testing.T) {
	got := parseEthtoolDriver([]byte("driver: mlx5_core\nversion: 5.0\nfirmware-version: 14.0\n"))
	if got != "mlx5_core" {
		t.Errorf("got %q, want mlx5_core", got)
	}
	if parseEthtoolDriver([]byte("no driver here\n")) != "" {
		t.Error("expected empty driver")
	}
}

func TestClassifyNativeXDP(t *testing.T) {
	cases := []struct {
		driver string
		want   xdpClass
	}{
		{"mlx5_core", xdpNativeLikely},
		{"ice", xdpNativeLikely},
		{"virtio_net", xdpNativeCaveat},
		{"veth", xdpNativeCaveat},
		{"bridge", xdpGenericOnly},
		{"weird_nic", xdpUnknown},
	}
	for _, c := range cases {
		got, _ := classifyNativeXDP(c.driver, "eth0", "")
		if got != c.want {
			t.Errorf("classifyNativeXDP(%q) = %v, want %v", c.driver, got, c.want)
		}
	}
}

func TestCheckInterfaceXDPFromSysfs(t *testing.T) {
	root := t.TempDir()
	ifDir := filepath.Join(root, "sys/class/net/eth0/device")
	if err := os.MkdirAll(ifDir, 0o755); err != nil {
		t.Fatal(err)
	}
	driverDir := filepath.Join(root, "sys/bus/pci/drivers/mlx5_core")
	if err := os.MkdirAll(driverDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driverDir, filepath.Join(ifDir, "driver")); err != nil {
		t.Fatal(err)
	}

	c := checkInterfaceXDPAt("eth0", root)
	if c.Status != StatusPass {
		t.Fatalf("status %s detail %q, want pass", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "mlx5_core") {
		t.Errorf("detail %q should name mlx5_core", c.Detail)
	}
}
