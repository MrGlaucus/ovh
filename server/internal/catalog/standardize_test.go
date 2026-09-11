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
