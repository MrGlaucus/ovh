package catalog

import "testing"

func TestFormatConfigDisplay(t *testing.T) {
	got := FormatConfigDisplay("ram-32g-ecc-2133", "softraid-4x2000sa")
	want := "32GB ECC RAM-2133 · 4×2TB SA"
	if got != want {
		t.Fatalf("配置展示 = %q，期望 %q", got, want)
	}
}

func TestFormatConfigDisplayKeepsExplicitMedium(t *testing.T) {
	got := FormatConfigDisplay("ram-64g-ecc-2400", "softraid-2x512nvme")
	want := "64GB ECC RAM-2400 · 2×512GB NVMe"
	if got != want {
		t.Fatalf("配置展示 = %q，期望 %q", got, want)
	}
}

func TestFormatMemoryDisplayDoesNotMislabelNoECC(t *testing.T) {
	if got, want := FormatMemoryDisplay("ram-128g-noecc-2933"), "128GB RAM-2933"; got != want {
		t.Fatalf("noecc 内存展示 = %q，期望 %q", got, want)
	}
}

func TestFormatOptionDisplay(t *testing.T) {
	cases := map[string]string{
		"ram-32g-ecc-2133-24sk20":    "32GB ECC RAM-2133",
		"softraid-2x450nvme-24sk20":  "2×450GB NVMe",
		"bandwidth-500-25sk":         "500 Mbps",
		"bandwidth-1000-24sk202":     "1 Gbps",
		"unrecognized-addon-24sk202": "unrecognized-addon-24sk202",
	}
	for input, want := range cases {
		if got := FormatOptionDisplay(input); got != want {
			t.Errorf("FormatOptionDisplay(%q) = %q，期望 %q", input, got, want)
		}
	}
}
