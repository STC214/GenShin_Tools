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
