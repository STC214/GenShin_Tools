package shell

import "testing"

func TestScalePluginContentYCompressesIntoAvailableHeight(t *testing.T) {
	const bottom int32 = 479
	if got := scalePluginContentY(170, 96, bottom); got != 170 {
		t.Fatalf("top = %d, want 170", got)
	}
	if got := scalePluginContentY(660, 96, bottom); got != bottom {
		t.Fatalf("bottom = %d, want %d", got, bottom)
	}
	previous := int32(-1)
	for logical := int32(170); logical <= 660; logical++ {
		got := scalePluginContentY(logical, 96, bottom)
		if got < previous || got > bottom {
			t.Fatalf("mapping is not monotonic/in bounds at %d: %d after %d", logical, got, previous)
		}
		previous = got
	}
}

func TestScalePluginContentYPreservesDesignAtLargeHeight(t *testing.T) {
	if got := scalePluginContentY(520, 144, 1000); got != 780 {
		t.Fatalf("scaled value = %d, want 780", got)
	}
}
