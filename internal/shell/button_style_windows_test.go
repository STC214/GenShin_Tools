package shell

import (
	"testing"

	"genshintools/internal/platform/win32"
)

func TestButtonIndexAtUsesHalfOpenBounds(t *testing.T) {
	rects := []win32.Rect{{Left: 10, Top: 20, Right: 30, Bottom: 40}, {Left: 35, Top: 20, Right: 55, Bottom: 40}}
	for _, test := range []struct {
		x, y int32
		want int
	}{{10, 20, 0}, {29, 39, 0}, {30, 20, -1}, {35, 20, 1}, {55, 20, -1}} {
		if got := buttonIndexAt(rects, test.x, test.y); got != test.want {
			t.Fatalf("buttonIndexAt(%d,%d)=%d want %d", test.x, test.y, got, test.want)
		}
	}
}

func TestSplitButtonRectAddsOnlyInteriorGaps(t *testing.T) {
	row := win32.Rect{Left: 100, Top: 20, Right: 400, Bottom: 60}
	first := splitButtonRect(row, 0, 3, 96)
	middle := splitButtonRect(row, 1, 3, 96)
	last := splitButtonRect(row, 2, 3, 96)
	if first.Left != row.Left || last.Right != row.Right || first.Right >= middle.Left || middle.Right >= last.Left {
		t.Fatalf("unexpected split cells: first=%+v middle=%+v last=%+v", first, middle, last)
	}
	if got := buttonCellAt(row, first.Right, 30, 3, 96); got != -1 {
		t.Fatalf("button gap resolved to cell %d, want -1", got)
	}
	if got := buttonCellAt(row, middle.Left, 30, 3, 96); got != 1 {
		t.Fatalf("middle button resolved to cell %d, want 1", got)
	}
}

func TestPluginHeaderGapIsNotInteractive(t *testing.T) {
	safe, target := pluginHeaderRects(100, 900, 20, 60, 96)
	if !pointInButton(safe, safe.Right-1, 30) || !pointInButton(target, target.Left, 30) {
		t.Fatalf("header buttons are not interactive: safe=%+v target=%+v", safe, target)
	}
	if pointInButton(safe, safe.Right, 30) || pointInButton(target, target.Left-1, 30) {
		t.Fatalf("header gap unexpectedly belongs to a button: safe=%+v target=%+v", safe, target)
	}
}

func TestHomeLaunchButtonsStayInsideContentAndKeepGap(t *testing.T) {
	client := win32.Rect{Right: 1100, Bottom: 720}
	clean, inject := homeLaunchRects(client, 96)
	if clean.Left < 252 || inject.Right != client.Right-42 || clean.Bottom != client.Bottom-58 {
		t.Fatalf("unexpected home launch layout: clean=%+v inject=%+v", clean, inject)
	}
	if got := buttonIndexAt([]win32.Rect{clean, inject}, clean.Right, clean.Top+1); got != -1 {
		t.Fatalf("home launch gap resolved to button %d", got)
	}
	clean, inject = homeLaunchRects(win32.Rect{Right: 700, Bottom: 420}, 96)
	if validButtonRect(clean) || validButtonRect(inject) {
		t.Fatalf("home launch buttons should hide when vertical space is insufficient: clean=%+v inject=%+v", clean, inject)
	}
	clean, inject = homeLaunchRects(win32.Rect{Right: 500, Bottom: 720}, 96)
	if validButtonRect(clean) || validButtonRect(inject) {
		t.Fatalf("home launch buttons should hide when horizontal space is insufficient: clean=%+v inject=%+v", clean, inject)
	}
}
