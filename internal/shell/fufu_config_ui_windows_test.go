package shell

import (
	"testing"

	"genshintools/internal/plugins"
)

func TestClampFufuScrollKeepsGeneratedListInRange(t *testing.T) {
	tests := []struct {
		current int
		delta   int
		total   int
		want    int
	}{
		{current: 0, delta: -3, total: 20, want: 0},
		{current: 0, delta: 3, total: 20, want: 3},
		{current: 12, delta: 3, total: 20, want: 14},
		{current: 4, delta: 3, total: 5, want: 0},
	}
	for _, test := range tests {
		if got := clampFufuScroll(test.current, test.delta, test.total); got != test.want {
			t.Fatalf("clampFufuScroll(%d, %d, %d)=%d, want %d", test.current, test.delta, test.total, got, test.want)
		}
	}
}

func TestActiveListStateCoversConfigInstalledAndStoreLists(t *testing.T) {
	app := application{selected: 8, pluginTargetMode: true, fufuTargetInstalled: true}
	app.fufuTarget.Settings = make([]plugins.FufuSetting, 9)
	position, total, visible, ok := app.activeListState()
	if !ok || position != &app.fufuScroll || total != 9 || visible != fufuVisibleRows {
		t.Fatalf("unexpected config list state: position=%p total=%d visible=%d ok=%v", position, total, visible, ok)
	}
	app.pluginTargetMode = false
	app.pluginItems = make([]plugins.Item, 7)
	position, total, visible, ok = app.activeListState()
	if !ok || position != &app.pluginListScroll || total != 7 || visible != pluginVisibleRows {
		t.Fatalf("unexpected installed list state: position=%p total=%d visible=%d ok=%v", position, total, visible, ok)
	}
	app.selected = 9
	app.pluginCatalogPage.Items = make([]plugins.CatalogItem, 5)
	position, total, visible, ok = app.activeListState()
	if !ok || position != &app.storeListScroll || total != 5 || visible != storeVisibleRows {
		t.Fatalf("unexpected store list state: position=%p total=%d visible=%d ok=%v", position, total, visible, ok)
	}
}

func TestFufuHeaderActionUsesHorizontalDPIScaling(t *testing.T) {
	selector, repair, toggle := fufuHeaderRects(100, 1000, 170, 224, 192)
	if !pointInButton(selector, 200, 200) || !pointInButton(repair, repair.Left, 200) || !pointInButton(toggle, toggle.Left, 200) {
		t.Fatalf("header actions do not scale with DPI: selector=%+v repair=%+v toggle=%+v", selector, repair, toggle)
	}
	if pointInButton(repair, repair.Left-1, 200) || pointInButton(toggle, toggle.Left-1, 200) {
		t.Fatalf("header visual gaps must not activate an action: repair=%+v toggle=%+v", repair, toggle)
	}
}
