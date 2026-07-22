package shell

import "genshintools/internal/platform/win32"

func buttonIndexAt(rects []win32.Rect, x, y int32) int {
	for index, rect := range rects {
		if pointInButton(rect, x, y) {
			return index
		}
	}
	return -1
}

func pointInButton(rect win32.Rect, x, y int32) bool {
	return x >= rect.Left && x < rect.Right && y >= rect.Top && y < rect.Bottom
}

func (app *application) beginButtonPaint() {
	app.buttonRects = app.buttonRects[:0]
	app.buttonShadow = win32.CreateSolidBrush(win32.Color(20, 23, 31))
	app.buttonPen = win32.CreatePen(win32.Color(52, 66, 112), 1)
	app.buttonHoverPen = win32.CreatePen(win32.Color(100, 132, 255), max(int32(1), win32.Scale(2, app.dpi)))
	app.buttonShadowPen = win32.CreatePen(win32.Color(20, 23, 31), 1)
}

func (app *application) endButtonPaint() {
	if app.buttonShadow != 0 {
		win32.DeleteObject(uintptr(app.buttonShadow))
		app.buttonShadow = 0
	}
	for _, pen := range []*uintptr{&app.buttonPen, &app.buttonHoverPen, &app.buttonShadowPen} {
		if *pen != 0 {
			win32.DeleteObject(*pen)
			*pen = 0
		}
	}
}

func (app *application) registerButtonRect(rect win32.Rect) bool {
	app.buttonRects = append(app.buttonRects, rect)
	return app.pointerInside && app.pointerX >= rect.Left && app.pointerX < rect.Right && app.pointerY >= rect.Top && app.pointerY < rect.Bottom
}

func (app *application) paintButtonSurface(dc win32.HDC, rect win32.Rect, fill win32.HBRUSH) {
	hovered := app.registerButtonRect(rect)
	app.paintModernSurface(dc, rect, fill, hovered)
}

func (app *application) paintStaticSurface(dc win32.HDC, rect win32.Rect, fill win32.HBRUSH) {
	app.paintModernSurface(dc, rect, fill, false)
}

func (app *application) paintModernSurface(dc win32.HDC, rect win32.Rect, fill win32.HBRUSH, hovered bool) {
	radius := max(int32(6), win32.Scale(10, app.dpi))
	pen := app.buttonPen
	if hovered {
		pen = app.buttonHoverPen
	}
	shadowRect := rect
	shadowRect.Top += win32.Scale(2, app.dpi)
	shadowRect.Bottom += win32.Scale(2, app.dpi)
	if app.buttonShadow != 0 {
		win32.DrawRoundedRectWithPen(dc, shadowRect, app.buttonShadow, app.buttonShadowPen, radius)
	}
	surface := rect
	surface.Bottom -= win32.Scale(2, app.dpi)
	win32.DrawRoundedRectWithPen(dc, surface, fill, pen, radius)
}

func (app *application) invalidateButtonHover(previous, current int) {
	for _, index := range []int{previous, current} {
		if index < 0 || index >= len(app.buttonRects) {
			continue
		}
		rect := app.buttonRects[index]
		padding := max(int32(2), win32.Scale(3, app.dpi))
		rect.Left -= padding
		rect.Top -= padding
		rect.Right += padding
		rect.Bottom += padding
		win32.InvalidateArea(app.hwnd, rect)
	}
}

func splitButtonRect(rect win32.Rect, index, count int32, dpi uint32) win32.Rect {
	width := (rect.Right - rect.Left) / count
	cell := win32.Rect{Left: rect.Left + index*width, Top: rect.Top, Right: rect.Left + (index+1)*width, Bottom: rect.Bottom}
	halfGap := max(int32(2), win32.Scale(3, dpi))
	if index > 0 {
		cell.Left += halfGap
	}
	if index < count-1 {
		cell.Right -= halfGap
	}
	return cell
}

func buttonCellAt(rect win32.Rect, x, y, count int32, dpi uint32) int {
	for index := int32(0); index < count; index++ {
		if pointInButton(splitButtonRect(rect, index, count, dpi), x, y) {
			return int(index)
		}
	}
	return -1
}

func fufuHeaderRects(left, right, top, bottom int32, dpi uint32) (win32.Rect, win32.Rect, win32.Rect) {
	actionLeft := right - win32.Scale(360, dpi)
	toggleLeft := right - win32.Scale(170, dpi)
	gap := win32.Scale(3, dpi)
	return win32.Rect{Left: left, Top: top, Right: actionLeft - gap, Bottom: bottom},
		win32.Rect{Left: actionLeft + gap, Top: top, Right: toggleLeft - gap, Bottom: bottom},
		win32.Rect{Left: toggleLeft + gap, Top: top, Right: right, Bottom: bottom}
}

func pluginHeaderRects(left, right, top, bottom int32, dpi uint32) (win32.Rect, win32.Rect) {
	boundary := right - win32.Scale(230, dpi)
	gap := win32.Scale(3, dpi)
	return win32.Rect{Left: left, Top: top, Right: boundary - gap, Bottom: bottom},
		win32.Rect{Left: boundary + gap, Top: top, Right: right, Bottom: bottom}
}

func homeLaunchRects(client win32.Rect, dpi uint32) (win32.Rect, win32.Rect) {
	right := client.Right - win32.Scale(42, dpi)
	bottom := client.Bottom - win32.Scale(58, dpi)
	top := bottom - win32.Scale(48, dpi)
	contentLeft := win32.Scale(252, dpi)
	if top < win32.Scale(354, dpi) || right-contentLeft < win32.Scale(260, dpi) {
		return win32.Rect{}, win32.Rect{}
	}
	row := win32.Rect{Left: max(contentLeft, right-win32.Scale(400, dpi)), Top: top, Right: right, Bottom: bottom}
	return splitButtonRect(row, 0, 2, dpi), splitButtonRect(row, 1, 2, dpi)
}

func validButtonRect(rect win32.Rect) bool {
	return rect.Right > rect.Left && rect.Bottom > rect.Top
}

func (app *application) paintNavigationSurface(dc win32.HDC, rect win32.Rect, selected bool) {
	hovered := app.registerButtonRect(rect)
	if !selected && !hovered {
		return
	}
	color := win32.Color(35, 40, 54)
	if selected {
		color = win32.Color(45, 51, 80)
	} else if hovered {
		color = win32.Color(25, 29, 39)
	}
	brush := win32.CreateSolidBrush(color)
	pen := app.buttonPen
	if hovered {
		pen = app.buttonHoverPen
	}
	win32.DrawRoundedRectWithPen(dc, rect, brush, pen, max(int32(6), win32.Scale(9, app.dpi)))
	win32.DeleteObject(uintptr(brush))
}
