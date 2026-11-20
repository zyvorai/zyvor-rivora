// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

package doctor

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Drivers that commonly support native (driver-mode) XDP on current kernels.
// This is a readiness hint for operators picking xdpMode: native|auto — the
// attach itself is still the source of truth (rivorad refuses native when the
// driver cannot). Keep the list conservative; unknown drivers stay informational.
var nativeXDPDrivers = map[string]string{
	"mlx5_core":  "Mellanox ConnectX",
	"mlx4_en":    "Mellanox ConnectX-3",
	"i40e":       "Intel X710/XL710",
	"ice":        "Intel E810",
	"ixgbe":      "Intel 82599/X550",
	"ixgbevf":    "Intel 82599/X550 VF",
	"bnxt_en":    "Broadcom NetXtreme",
	"nfp":        "Netronome",
	"qede":       "QLogic FastLinQ",
	"ena":        "AWS Elastic Network Adapter",
	"gve":        "Google Virtual NIC",
	"hv_netvsc":  "Hyper-V netvsc",
	"mana":       "Azure MANA",
	"atlantic":   "Aquantia AQtion",
	"virtio_net": "virtio-net",
}

// Drivers / kinds that should stay on generic XDP for Rivora.
var genericOnlyDrivers = map[string]string{
	"bridge":  "Linux bridge",
	"bonding": "Linux bonding (use a slave's native mode instead)",
	"tun":     "TUN",
	"tap":     "TAP",
	"dummy":   "dummy",
	"veth":    "veth",
	"lo":      "loopback",
}

type xdpClass int

const (
	xdpNativeLikely xdpClass = iota
	xdpNativeCaveat
	xdpGenericOnly
	xdpUnknown
)

func checkInterfaceXDP(iface string) Check {
	return checkInterfaceXDPAt(iface, "")
}

func checkInterfaceXDPAt(iface, root string) Check {
	title := "Native XDP (" + iface + ")"
	driver, detail, err := interfaceDriver(iface, root)
	if err != nil {
		return Check{
			Status: StatusInfo, Title: title,
			Detail:      err.Error(),
			Remediation: "install ethtool, or pass a real interface name; generic/skb mode still works",
		}
	}

	class, note := classifyNativeXDP(driver, iface, root)
	switch class {
	case xdpNativeLikely:
		return Check{
			Status: StatusPass, Title: title,
			Detail: fmt.Sprintf("driver %q (%s) — native XDP is likely supported; xdpMode: native or auto is reasonable", driver, note),
		}
	case xdpNativeCaveat:
		return Check{
			Status: StatusWarn, Title: title,
			Detail:      fmt.Sprintf("driver %q — %s", driver, note),
			Remediation: "prefer xdpMode: auto (falls back to generic) or generic on this interface; see operations/runbook.md#xdp-attach-mode-generic-native-auto",
		}
	case xdpGenericOnly:
		return Check{
			Status: StatusInfo, Title: title,
			Detail: fmt.Sprintf("driver %q (%s) — use xdpMode: generic; native is not appropriate here", driver, note),
		}
	default:
		return Check{
			Status: StatusInfo, Title: title,
			Detail: fmt.Sprintf("driver %q — native XDP support unknown; try xdpMode: auto (native then generic). %s", driver, detail),
		}
	}
}

func classifyNativeXDP(driver, iface, root string) (xdpClass, string) {
	d := strings.ToLower(strings.TrimSpace(driver))
	if note, ok := genericOnlyDrivers[d]; ok {
		if d == "veth" {
			return xdpNativeCaveat, "veth accepts a native attach, but XDP_TX across a bridge is unreliable; Rivora defaults to generic on veth-like setups"
		}
		return xdpGenericOnly, note
	}
	if note, ok := nativeXDPDrivers[d]; ok {
		if d == "virtio_net" {
			return xdpNativeCaveat, "virtio-net often supports native XDP, but guest/hypervisor versions vary; prefer xdpMode: auto"
		}
		return xdpNativeLikely, note
	}
	// Kind from sysfs when the driver name is unfamiliar.
	if kind := netdevKind(iface, root); kind != "" {
		if note, ok := genericOnlyDrivers[kind]; ok {
			if kind == "veth" {
				return xdpNativeCaveat, "interface kind is veth — native XDP_TX across a bridge is unreliable; use generic or auto"
			}
			return xdpGenericOnly, note
		}
	}
	return xdpUnknown, ""
}

func interfaceDriver(iface, root string) (driver, detail string, err error) {
	if root == "" {
		root = "/"
	}
	if link, e := os.Readlink(filepath.Join(root, "sys/class/net", iface, "device/driver")); e == nil {
		driver = filepath.Base(link)
		detail = "from sysfs device/driver"
		return driver, detail, nil
	}
	out, e := exec.Command("ethtool", "-i", iface).CombinedOutput()
	if e != nil {
		return "", "", fmt.Errorf("ethtool -i %s failed (%v) and no sysfs driver link — cannot classify native XDP", iface, e)
	}
	driver = parseEthtoolDriver(out)
	if driver == "" {
		return "", "", fmt.Errorf("ethtool -i %s returned no driver field", iface)
	}
	return driver, "from ethtool -i", nil
}

func parseEthtoolDriver(out []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "driver:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "driver:"))
		}
	}
	return ""
}

func netdevKind(iface, root string) string {
	if root == "" {
		root = "/"
	}
	// uevent DEVTYPE=... is present for some virtual devices.
	b, err := os.ReadFile(filepath.Join(root, "sys/class/net", iface, "uevent"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "DEVTYPE=") {
			return strings.TrimPrefix(line, "DEVTYPE=")
		}
	}
	return ""
}
