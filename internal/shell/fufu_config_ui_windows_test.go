package shell

import (
	"strings"
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

func TestFufuPluginSelectorShowsFullBuildVersionFirst(t *testing.T) {
	format := "配置目标：FuFuPlugin（主插件）  |  %s"
	target := plugins.FufuTargetConfig{
		Name: "FufuLauncher-Plugin", Description: "9月25号构建版本 游戏7.1 版本1.7.0.2",
		Version: "1.7.0", Developer: "ME46231",
	}
	want := "1.7.0.2  |  配置目标：FuFuPlugin（主插件）  |  FufuLauncher-Plugin  |  ME46231"
	if got := fufuPluginSelectorText(format, "未安装", target, true); got != want {
		t.Fatalf("selector text = %q, want %q", got, want)
	}
	target.Description = "build without a full version"
	if got := fufuPluginSelectorText(format, "未安装", target, true); !strings.HasPrefix(got, "1.7.0  |  ") {
		t.Fatalf("selector did not fall back to the plugin version: %q", got)
	}
	if got := fufuPluginSelectorText(format, "未安装", target, false); got != "配置目标：FuFuPlugin（主插件）  |  未安装" {
		t.Fatalf("uninstalled selector text = %q", got)
	}
}
