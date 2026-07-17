// Package shell implements the stable S02 Windows shell and lifetime boundary.
package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"genshintools/internal/buildinfo"
	"genshintools/internal/config"
	"genshintools/internal/diagnostics"
	"genshintools/internal/input"
	"genshintools/internal/paths"
	"genshintools/internal/platform/win32"
	"genshintools/internal/taskrunner"
)

const (
	windowClass  = "GenshinTools.MainWindow.S02"
	instanceName = "Local\\GenshinTools.Singleton.S02"

	messageActivate = win32.WM_APP + 1
	messageTray     = win32.WM_APP + 2
	messageSnapshot = win32.WM_APP + 3
	messageInput    = win32.WM_APP + 4
	messagePhysical = win32.WM_APP + 5

	trayID   = 1
	menuShow = 1001
	menuExit = 1002
)

var active *application

type application struct {
	hwnd        win32.HWND
	instance    win32.HINSTANCE
	icon        win32.HICON
	dpi         uint32
	selected    int
	lastBounds  config.WindowConfig
	settings    config.Settings
	layout      paths.Layout
	build       buildinfo.Info
	logger      *diagnostics.Logger
	tasks       *taskrunner.Manager
	tray        win32.NotifyIconData
	trayAdded   bool
	taskbarMsg  uint32
	previousBad bool
	recovered   string

	fontTitle win32.HFONT
	fontBody  win32.HFONT
	fontNav   win32.HFONT

	snapshots            chan win32.ResourceSnapshot
	lastSnap             win32.ResourceSnapshot
	inputNative          *input.Native
	inputUpdates         chan input.Snapshot
	physicalEvents       chan input.PhysicalEvent
	inputSnap            input.Snapshot
	recording            int
	inputUIError         string
	sessionNotifications bool
	shutdown             sync.Once
	cleanExit            bool
	fatal                bool
}

var navigation = []struct{ title, subtitle string }{
	{"首页", "启动状态与常用操作将在后续阶段接入。"},
	{"游戏管理", "S04 将实现路径发现、版本和区服识别。"},
	{"输入增强", "S03 将优先实现鼠标连点与键盘连按。"},
	{"插件", "S09/S10 才会启用注入与插件，当前保持隔离。"},
	{"设置", "S02 已启用便携配置、日志、DPI 和安全退出。"},
}

func Run(layout paths.Layout, build buildinfo.Info) (returnErr error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	win32.EnablePerMonitorV2()
	if err := win32.InitializeCOM(); err != nil {
		return err
	}
	defer win32.UninitializeCOM()

	mutex, alreadyRunning, err := win32.CreateSingleInstanceMutex(instanceName)
	if err != nil {
		return fmt.Errorf("create single-instance mutex: %w", err)
	}
	defer win32.CloseHandle(mutex)
	if alreadyRunning {
		activateExistingInstance()
		return nil
	}

	logger, err := diagnostics.Open(layout.Logs)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Close() }()

	markerPath := layout.Data + string(os.PathSeparator) + "session.marker"
	previousBad, err := diagnostics.BeginSession(markerPath, build.Version)
	if err != nil {
		return fmt.Errorf("begin diagnostic session: %w", err)
	}

	defer func() {
		if value := recover(); value != nil {
			logger.Panic("panic escaped Win32 shell", value)
			returnErr = fmt.Errorf("Win32 shell panic: %v", value)
		}
	}()

	loaded, err := config.Load(layout.Config)
	if err != nil {
		return err
	}
	app := &application{
		instance:       win32.ModuleHandle(),
		settings:       loaded.Settings,
		lastBounds:     loaded.Settings.Window,
		layout:         layout,
		build:          build,
		logger:         logger,
		tasks:          taskrunner.New(),
		previousBad:    previousBad,
		recovered:      loaded.RecoveredFrom,
		snapshots:      make(chan win32.ResourceSnapshot, 1),
		inputUpdates:   make(chan input.Snapshot, 1),
		physicalEvents: make(chan input.PhysicalEvent, 16),
	}
	active = app
	defer func() { active = nil }()

	logger.Info("application starting", map[string]any{"version": build.Version, "commit": build.Commit, "previousUncleanExit": previousBad})
	if loaded.RecoveredFrom != "" {
		logger.Error("corrupt settings quarantined", map[string]any{"path": loaded.RecoveredFrom})
	}

	if err := app.createWindow(); err != nil {
		return err
	}
	if err := app.startInput(); err != nil {
		app.requestShutdown()
		return fmt.Errorf("start input enhancement: %w", err)
	}
	app.startBackgroundDiagnostics()
	app.startSmokeHooks()

	var message win32.Msg
	for {
		result, err := win32.GetMessage(&message)
		if err != nil {
			return fmt.Errorf("GetMessageW: %w", err)
		}
		if result == 0 {
			break
		}
		win32.TranslateMessage(&message)
		win32.DispatchMessage(&message)
	}

	if app.cleanExit && !app.fatal {
		if err := diagnostics.EndSession(markerPath); err != nil {
			logger.Error("remove clean-session marker", map[string]any{"error": err.Error()})
		}
	}
	logger.Info("application stopped", map[string]any{"clean": app.cleanExit, "fatal": app.fatal})
	if app.fatal {
		return errors.New("fatal error in window procedure; see data/logs/genshin-tools.log")
	}
	return nil
}

func activateExistingInstance() {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hwnd := win32.FindWindow(windowClass); hwnd != 0 {
			win32.PostMessage(hwnd, messageActivate, 0, 0)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (app *application) createWindow() error {
	className := win32.UTF16(windowClass)
	app.icon = win32.LoadIcon(app.instance, 1)
	class := win32.WndClassEx{
		Style:     win32.CS_HREDRAW | win32.CS_VREDRAW | 0x0008,
		WndProc:   win32.NewCallback(windowProcedure),
		Instance:  app.instance,
		Icon:      app.icon,
		Cursor:    win32.LoadArrowCursor(),
		ClassName: className,
		IconSmall: app.icon,
	}
	if err := win32.RegisterClass(&class); err != nil {
		return err
	}

	bounds := clampBounds(app.settings.Window)
	app.lastBounds = bounds
	title := win32.UTF16("Genshin Tools " + app.build.Version)
	hwnd, err := win32.CreateWindow(className, title, int32(bounds.X), int32(bounds.Y), int32(bounds.Width), int32(bounds.Height), app.instance)
	if err != nil {
		return err
	}
	app.hwnd = hwnd
	app.dpi = win32.DPIForWindow(hwnd)
	app.recreateFonts()
	win32.EnableDarkTitleBar(hwnd)
	app.taskbarMsg = win32.RegisterWindowMessage("TaskbarCreated")
	app.addTrayIcon()
	win32.ShowWindow(hwnd, win32.SW_SHOWNORMAL)
	win32.UpdateWindow(hwnd)
	return nil
}

func clampBounds(window config.WindowConfig) config.WindowConfig {
	if window.Width < 840 {
		window.Width = 840
	}
	if window.Height < 560 {
		window.Height = 560
	}
	probe := win32.Rect{Left: int32(window.X), Top: int32(window.Y), Right: int32(window.X + window.Width), Bottom: int32(window.Y + window.Height)}
	if window.X < 0 || window.Y < 0 {
		probe = win32.Rect{Left: 0, Top: 0, Right: int32(window.Width), Bottom: int32(window.Height)}
	}
	work := win32.WorkAreaFor(probe)
	workWidth, workHeight := int(work.Right-work.Left), int(work.Bottom-work.Top)
	if window.Width > workWidth {
		window.Width = workWidth
	}
	if window.Height > workHeight {
		window.Height = workHeight
	}
	if window.X < int(work.Left) || window.X+window.Width > int(work.Right) {
		window.X = int(work.Left) + (workWidth-window.Width)/2
	}
	if window.Y < int(work.Top) || window.Y+window.Height > int(work.Bottom) {
		window.Y = int(work.Top) + (workHeight-window.Height)/2
	}
	return window
}

func windowProcedure(hwnd uintptr, message uint32, wParam, lParam uintptr) (result uintptr) {
	defer func() {
		if value := recover(); value != nil {
			if active != nil {
				active.fatal = true
				active.logger.Panic("panic in window procedure", value)
			}
			win32.DestroyWindow(win32.HWND(hwnd))
			result = 0
		}
	}()
	if active == nil {
		return win32.DefWindowProc(win32.HWND(hwnd), message, wParam, lParam)
	}
	return active.handleMessage(win32.HWND(hwnd), message, wParam, lParam)
}

func (app *application) handleMessage(hwnd win32.HWND, message uint32, wParam, lParam uintptr) uintptr {
	if message == app.taskbarMsg && app.taskbarMsg != 0 {
		app.addTrayIcon()
		return 0
	}
	switch message {
	case win32.WM_CREATE:
		return 0
	case messageActivate:
		app.restore()
		if path := os.Getenv("GENSHINTOOLS_S02_ACTIVATED_FILE"); path != "" {
			_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
		}
		return 0
	case messageSnapshot:
		select {
		case app.lastSnap = <-app.snapshots:
		default:
		}
		win32.Invalidate(hwnd)
		return 0
	case messageInput:
		for {
			select {
			case app.inputSnap = <-app.inputUpdates:
			default:
				win32.Invalidate(hwnd)
				return 0
			}
		}
	case messagePhysical:
		for {
			select {
			case event := <-app.physicalEvents:
				app.recordPhysical(event)
			default:
				return 0
			}
		}
	case messageTray:
		event := uint32(lParam & 0xffff)
		switch event {
		case win32.WM_LBUTTONDBLCLK:
			app.restore()
		case win32.WM_RBUTTONUP:
			switch win32.ShowTrayMenu(hwnd, menuShow, menuExit) {
			case menuShow:
				app.restore()
			case menuExit:
				app.requestShutdown()
			}
		}
		return 0
	case win32.WM_LBUTTONDOWN:
		x, y := int(int16(lParam&0xffff)), int(int16((lParam>>16)&0xffff))
		if selected := app.navigationAt(x, y); selected >= 0 && selected != app.selected {
			app.selected = selected
			win32.Invalidate(hwnd)
		} else if app.selected == 2 && x >= int(win32.Scale(210, app.dpi)) {
			app.inputClick(x, y)
		}
		return 0
	case win32.WM_KEYDOWN:
		switch wParam {
		case win32.VK_UP:
			if app.selected > 0 {
				app.selected--
				win32.Invalidate(hwnd)
			}
		case win32.VK_DOWN:
			if app.selected < len(navigation)-1 {
				app.selected++
				win32.Invalidate(hwnd)
			}
		}
		return 0
	case win32.WM_PAINT:
		app.paint(hwnd)
		return 0
	case win32.WM_ERASEBKGND:
		return 1
	case win32.WM_MOVE:
		app.captureBounds()
		return 0
	case win32.WM_SIZE:
		if wParam == win32.SIZE_MINIMIZED {
			win32.ShowWindow(hwnd, win32.SW_HIDE)
		} else {
			app.captureBounds()
			win32.Invalidate(hwnd)
		}
		return 0
	case win32.WM_DPICHANGED:
		app.dpi = uint32(wParam & 0xffff)
		app.recreateFonts()
		suggested := *(*win32.Rect)(unsafe.Pointer(lParam))
		win32.SetWindowPos(hwnd, suggested, win32.SWP_NOZORDER|win32.SWP_NOACTIVATE)
		return 0
	case win32.WM_GETMINMAXINFO:
		info := (*win32.MinMaxInfo)(unsafe.Pointer(lParam))
		info.MinTrackSize = win32.Point{X: win32.Scale(840, app.dpi), Y: win32.Scale(560, app.dpi)}
		return 0
	case win32.WM_QUERYENDSESSION:
		return 1
	case win32.WM_ENDSESSION:
		if wParam != 0 {
			app.requestShutdown()
		}
		return 0
	case win32.WM_POWERBROADCAST:
		if wParam == win32.PBT_APMSUSPEND || wParam == win32.PBT_APMRESUMEAUTOMATIC {
			app.stopInputForSystemEvent("power transition")
		}
		return 1
	case win32.WM_WTSSESSIONCHANGE:
		if wParam == win32.WTS_SESSION_LOCK || wParam == win32.WTS_SESSION_UNLOCK {
			app.stopInputForSystemEvent("session transition")
		}
		return 0
	case win32.WM_CLOSE:
		app.requestShutdown()
		return 0
	case win32.WM_DESTROY:
		win32.PostQuitMessage(0)
		return 0
	}
	return win32.DefWindowProc(hwnd, message, wParam, lParam)
}

func (app *application) addTrayIcon() {
	if app.trayAdded {
		win32.DeleteTrayIcon(&app.tray)
	}
	app.tray = win32.NotifyIconData{Window: app.hwnd, ID: trayID, Flags: win32.NIF_MESSAGE | win32.NIF_ICON | win32.NIF_TIP, CallbackMsg: messageTray, Icon: app.icon}
	win32.CopyUTF16(app.tray.Tip[:], "Genshin Tools "+app.build.Version)
	app.trayAdded = win32.AddTrayIcon(&app.tray)
	if app.trayAdded {
		app.logger.Info("tray icon added", nil)
	} else {
		app.logger.Error("add tray icon failed", nil)
	}
}

func (app *application) restore() {
	win32.ShowWindow(app.hwnd, win32.SW_RESTORE)
	win32.SetForegroundWindow(app.hwnd)
	win32.Invalidate(app.hwnd)
}

func (app *application) requestShutdown() {
	app.shutdown.Do(func() {
		app.captureBounds()
		app.settings.Window = app.lastBounds
		if err := config.Save(app.layout.Config, app.settings); err != nil {
			app.logger.Error("save settings", map[string]any{"error": err.Error()})
		}
		if app.trayAdded {
			win32.DeleteTrayIcon(&app.tray)
			app.trayAdded = false
		}
		if app.inputNative != nil {
			app.inputNative.Close()
			app.inputNative = nil
		}
		if app.sessionNotifications {
			win32.UnregisterSessionNotifications(app.hwnd)
			app.sessionNotifications = false
		}
		if !app.tasks.Shutdown(2 * time.Second) {
			app.logger.Error("background task shutdown timed out", nil)
		}
		app.deleteFonts()
		app.cleanExit = true
		win32.DestroyWindow(app.hwnd)
	})
}

func (app *application) captureBounds() {
	if app.hwnd == 0 || win32.IsIconic(app.hwnd) || !win32.IsVisible(app.hwnd) {
		return
	}
	if rect, ok := win32.GetWindowRect(app.hwnd); ok {
		width, height := int(rect.Right-rect.Left), int(rect.Bottom-rect.Top)
		if width >= 640 && height >= 480 {
			app.lastBounds = config.WindowConfig{X: int(rect.Left), Y: int(rect.Top), Width: width, Height: height}
		}
	}
}

func (app *application) recreateFonts() {
	app.deleteFonts()
	app.fontTitle = win32.CreateFont(-win32.Scale(30, app.dpi), 600, "Segoe UI")
	app.fontBody = win32.CreateFont(-win32.Scale(16, app.dpi), 400, "Segoe UI")
	app.fontNav = win32.CreateFont(-win32.Scale(16, app.dpi), 500, "Microsoft YaHei UI")
}

func (app *application) deleteFonts() {
	win32.DeleteObject(uintptr(app.fontTitle))
	win32.DeleteObject(uintptr(app.fontBody))
	win32.DeleteObject(uintptr(app.fontNav))
	app.fontTitle, app.fontBody, app.fontNav = 0, 0, 0
}

func (app *application) navigationAt(x, y int) int {
	if x < 0 || x >= int(win32.Scale(210, app.dpi)) {
		return -1
	}
	start, height := int(win32.Scale(88, app.dpi)), int(win32.Scale(48, app.dpi))
	index := (y - start) / height
	if y < start || index < 0 || index >= len(navigation) {
		return -1
	}
	return index
}

func (app *application) paint(hwnd win32.HWND) {
	var paint win32.PaintStruct
	dc := win32.BeginPaint(hwnd, &paint)
	defer win32.EndPaint(hwnd, &paint)
	client := win32.GetClientRect(hwnd)
	background := win32.CreateSolidBrush(win32.Color(15, 17, 23))
	defer win32.DeleteObject(uintptr(background))
	sidebar := win32.CreateSolidBrush(win32.Color(24, 27, 36))
	defer win32.DeleteObject(uintptr(sidebar))
	selected := win32.CreateSolidBrush(win32.Color(45, 51, 80))
	defer win32.DeleteObject(uintptr(selected))
	accent := win32.CreateSolidBrush(win32.Color(100, 132, 255))
	defer win32.DeleteObject(uintptr(accent))
	status := win32.CreateSolidBrush(win32.Color(20, 23, 31))
	defer win32.DeleteObject(uintptr(status))

	win32.FillRect(dc, &client, background)
	sidebarWidth := win32.Scale(210, app.dpi)
	sidebarRect := win32.Rect{Left: 0, Top: 0, Right: sidebarWidth, Bottom: client.Bottom}
	win32.FillRect(dc, &sidebarRect, sidebar)
	statusHeight := win32.Scale(34, app.dpi)
	statusRect := win32.Rect{Left: sidebarWidth, Top: client.Bottom - statusHeight, Right: client.Right, Bottom: client.Bottom}
	win32.FillRect(dc, &statusRect, status)
	win32.SetTransparentBackground(dc)

	old := win32.SelectObject(dc, uintptr(app.fontNav))
	win32.SetTextColor(dc, win32.Color(235, 238, 248))
	logo := win32.Rect{Left: win32.Scale(22, app.dpi), Top: win32.Scale(24, app.dpi), Right: sidebarWidth - win32.Scale(12, app.dpi), Bottom: win32.Scale(66, app.dpi)}
	win32.DrawText(dc, "GENSHIN TOOLS", &logo, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE)
	start, itemHeight := win32.Scale(88, app.dpi), win32.Scale(48, app.dpi)
	for index, item := range navigation {
		top := start + int32(index)*itemHeight
		row := win32.Rect{Left: win32.Scale(10, app.dpi), Top: top, Right: sidebarWidth - win32.Scale(10, app.dpi), Bottom: top + itemHeight - win32.Scale(4, app.dpi)}
		if index == app.selected {
			win32.FillRect(dc, &row, selected)
			bar := win32.Rect{Left: row.Left, Top: row.Top + win32.Scale(8, app.dpi), Right: row.Left + win32.Scale(3, app.dpi), Bottom: row.Bottom - win32.Scale(8, app.dpi)}
			win32.FillRect(dc, &bar, accent)
		}
		row.Left += win32.Scale(18, app.dpi)
		win32.DrawText(dc, item.title, &row, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE)
	}

	contentLeft := sidebarWidth + win32.Scale(42, app.dpi)
	win32.SelectObject(dc, uintptr(app.fontTitle))
	titleRect := win32.Rect{Left: contentLeft, Top: win32.Scale(52, app.dpi), Right: client.Right - win32.Scale(30, app.dpi), Bottom: win32.Scale(104, app.dpi)}
	win32.DrawText(dc, navigation[app.selected].title, &titleRect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE)
	win32.SelectObject(dc, uintptr(app.fontBody))
	win32.SetTextColor(dc, win32.Color(166, 174, 197))
	subtitle := navigation[app.selected].subtitle
	if app.previousBad {
		subtitle += "  已检测到上次异常退出。"
	}
	if app.recovered != "" {
		subtitle += "  已隔离损坏配置。"
	}
	subtitleRect := win32.Rect{Left: contentLeft, Top: win32.Scale(112, app.dpi), Right: client.Right - win32.Scale(30, app.dpi), Bottom: win32.Scale(158, app.dpi)}
	win32.DrawText(dc, subtitle, &subtitleRect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)

	if app.selected == 2 {
		app.paintInput(dc, client, contentLeft)
	} else {
		cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
		defer win32.DeleteObject(uintptr(cardBrush))
		card := win32.Rect{Left: contentLeft, Top: win32.Scale(184, app.dpi), Right: client.Right - win32.Scale(42, app.dpi), Bottom: win32.Scale(330, app.dpi)}
		win32.FillRect(dc, &card, cardBrush)
		win32.SetTextColor(dc, win32.Color(225, 229, 242))
		card.Left += win32.Scale(24, app.dpi)
		card.Top += win32.Scale(18, app.dpi)
		card.Right -= win32.Scale(20, app.dpi)
		card.Bottom = card.Top + win32.Scale(34, app.dpi)
		win32.DrawText(dc, "S02 稳定外壳运行中", &card, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE)
		win32.SetTextColor(dc, win32.Color(145, 154, 180))
		card.Top += win32.Scale(42, app.dpi)
		card.Bottom += win32.Scale(66, app.dpi)
		win32.DrawText(dc, "已启用：单实例 · 托盘 · DPI · 原子配置 · JSON 日志 · 安全退出", &card, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}

	win32.SetTextColor(dc, win32.Color(126, 136, 160))
	win32.SelectObject(dc, uintptr(app.fontBody))
	statusRect.Left += win32.Scale(16, app.dpi)
	statusText := fmt.Sprintf("v%s  |  DPI %d  |  Goroutines %d  |  Threads %d  |  Handles %d  |  USER %d  |  GDI %d", app.build.Version, app.dpi, runtime.NumGoroutine(), app.lastSnap.Threads, app.lastSnap.Handles, app.lastSnap.USER, app.lastSnap.GDI)
	win32.DrawText(dc, statusText, &statusRect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	win32.SelectObject(dc, old)
}

func (app *application) startInput() error {
	native, err := input.NewNative(func(snapshot input.Snapshot) {
		select {
		case app.inputUpdates <- snapshot:
		default:
			select {
			case <-app.inputUpdates:
			default:
			}
			select {
			case app.inputUpdates <- snapshot:
			default:
			}
		}
		win32.PostMessage(app.hwnd, messageInput, 0, 0)
	})
	if err != nil {
		return err
	}
	native.SetObserver(func(event input.PhysicalEvent) {
		select {
		case app.physicalEvents <- event:
			win32.PostMessage(app.hwnd, messagePhysical, 0, 0)
		default:
		}
	})
	if err := native.Start(); err != nil {
		native.Close()
		return err
	}
	if err := native.Configure(app.settings.Input); err != nil {
		native.Close()
		return err
	}
	app.inputNative = native
	app.inputSnap = native.Snapshot()
	app.sessionNotifications = win32.RegisterSessionNotifications(app.hwnd)
	if !app.sessionNotifications {
		app.logger.Error("register session notifications failed", nil)
	}
	app.logger.Info("input enhancement initialized", map[string]any{"state": app.inputSnap.State.String()})
	return nil
}

func (app *application) stopInputForSystemEvent(reason string) {
	if app.inputNative == nil {
		return
	}
	app.inputNative.Enable(false)
	app.settings.Input = app.inputNative.Snapshot().Config
	app.settings.Input.Enabled = false
	app.saveInputSettings()
	app.logger.Info("input enhancement stopped", map[string]any{"reason": reason})
}

func (app *application) saveInputSettings() {
	settings := app.settings
	settings.Input.Enabled = false
	if err := config.Save(app.layout.Config, settings); err != nil {
		app.logger.Error("save input settings", map[string]any{"error": err.Error()})
	}
}

func (app *application) inputClick(x, y int) {
	if app.inputNative == nil {
		return
	}
	left := int(win32.Scale(252, app.dpi))
	if x < left {
		return
	}
	config := app.inputNative.Snapshot().Config
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	switch {
	case y >= sy(170) && y < sy(216):
		app.recording = 0
		app.inputUIError = ""
		app.inputNative.Enable(!config.Enabled)
	case y >= sy(226) && y < sy(266):
		mode := (x - left) / max(1, int(win32.Scale(132, app.dpi)))
		if mode >= 0 && mode <= int(input.ModeMouseRight) {
			config.Mode = input.Mode(mode)
			config.Enabled = false
			if err := app.inputNative.Configure(config); err != nil {
				app.inputUIError = err.Error()
				app.logger.Error("configure input mode", map[string]any{"error": err.Error()})
			} else {
				app.inputUIError = ""
			}
		}
	case y >= sy(276) && y < sy(316) && config.Mode == input.ModeKeyboard:
		app.inputNative.Enable(false)
		app.recording = 1
	case y >= sy(326) && y < sy(366) && config.Mode == input.ModeKeyboard:
		app.inputNative.Enable(false)
		app.recording = 2
	case y >= sy(376) && y < sy(416):
		app.inputNative.Enable(false)
		app.recording = 3
	case y >= sy(426) && y < sy(466):
		if x < left+int(win32.Scale(200, app.dpi)) {
			config.Interval -= 10 * time.Millisecond
		} else {
			config.Interval += 10 * time.Millisecond
		}
		config.IntervalMS = int(config.Interval / time.Millisecond)
		config.Enabled = false
		if err := app.inputNative.Configure(config); err != nil {
			app.inputUIError = err.Error()
			app.logger.Error("configure input interval", map[string]any{"error": err.Error()})
		} else {
			app.inputUIError = ""
		}
	}
	app.settings.Input = app.inputNative.Snapshot().Config
	app.saveInputSettings()
	win32.Invalidate(app.hwnd)
}

func (app *application) recordPhysical(event input.PhysicalEvent) {
	if app.recording == 0 || !event.Down || event.Kind != input.EventKey || app.inputNative == nil {
		return
	}
	config := app.inputNative.Snapshot().Config
	switch app.recording {
	case 1:
		config.TriggerKey = event.Code
	case 2:
		config.OutputKey = event.Code
	case 3:
		config.StopKey = event.Code
	}
	config.Enabled = false
	app.recording = 0
	if err := app.inputNative.Configure(config); err != nil {
		app.inputUIError = err.Error()
		app.logger.Error("record input hotkey", map[string]any{"error": err.Error()})
		win32.Invalidate(app.hwnd)
		return
	}
	app.inputUIError = ""
	app.settings.Input = app.inputNative.Snapshot().Config
	app.saveInputSettings()
	win32.Invalidate(app.hwnd)
}

func (app *application) paintInput(dc win32.HDC, client win32.Rect, left int32) {
	cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
	defer win32.DeleteObject(uintptr(cardBrush))
	activeBrush := win32.CreateSolidBrush(win32.Color(52, 66, 112))
	defer win32.DeleteObject(uintptr(activeBrush))
	buttonBrush := win32.CreateSolidBrush(win32.Color(35, 40, 54))
	defer win32.DeleteObject(uintptr(buttonBrush))
	greenBrush := win32.CreateSolidBrush(win32.Color(42, 139, 103))
	defer win32.DeleteObject(uintptr(greenBrush))

	snapshot := app.inputSnap
	if app.inputNative != nil {
		snapshot = app.inputNative.Snapshot()
	}
	config := snapshot.Config
	right := client.Right - win32.Scale(42, app.dpi)
	row := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		win32.FillRect(dc, &rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}

	toggleBrush := cardBrush
	if config.Enabled {
		toggleBrush = greenBrush
	}
	action := "启用"
	if config.Enabled {
		action = "禁用"
	}
	draw(fmt.Sprintf("输入增强：%s    单击%s", inputStateText(snapshot.State), action), row(170, 216, toggleBrush), win32.Color(235, 238, 248))

	modeWidth := win32.Scale(132, app.dpi)
	modeNames := []string{"键盘连按", "左键连点", "右键连点"}
	for index, name := range modeNames {
		rect := win32.Rect{Left: left + int32(index)*modeWidth, Top: win32.Scale(226, app.dpi), Right: left + int32(index+1)*modeWidth - win32.Scale(8, app.dpi), Bottom: win32.Scale(266, app.dpi)}
		brush := buttonBrush
		if int(config.Mode) == index {
			brush = activeBrush
		}
		win32.FillRect(dc, &rect, brush)
		draw(name, rect, win32.Color(225, 229, 242))
	}
	if config.Mode == input.ModeKeyboard {
		draw(recordLabel("触发键", config.TriggerKey, app.recording == 1), row(276, 316, buttonBrush), win32.Color(225, 229, 242))
		draw(recordLabel("输出键", config.OutputKey, app.recording == 2), row(326, 366, buttonBrush), win32.Color(225, 229, 242))
	} else {
		draw("触发方式：按住对应物理鼠标键", row(276, 366, cardBrush), win32.Color(166, 174, 197))
	}
	draw(recordLabel("全局停止键", config.StopKey, app.recording == 3), row(376, 416, buttonBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf("－       间隔 %d ms       ＋", config.IntervalMS), row(426, 466, buttonBrush), win32.Color(225, 229, 242))
	visibleError := snapshot.LastError
	if visibleError == "" {
		visibleError = app.inputUIError
	}
	if visibleError != "" {
		draw("错误："+visibleError, row(476, 516, cardBrush), win32.Color(255, 126, 126))
	} else {
		draw(fmt.Sprintf("已输出 %d 组完整 down/up；重启后默认保持禁用", snapshot.OutputCount), row(476, 516, cardBrush), win32.Color(145, 154, 180))
	}
}

func inputStateText(state input.State) string {
	switch state {
	case input.StateDisabled:
		return "已禁用"
	case input.StateArmed:
		return "待触发"
	case input.StateRunning:
		return "运行中"
	case input.StateStopping:
		return "停止中"
	case input.StateFault:
		return "故障"
	default:
		return "未知"
	}
}

func recordLabel(name string, key uint32, recording bool) string {
	if recording {
		return name + "：请按下新按键…"
	}
	return fmt.Sprintf("%s：%s    单击录制", name, virtualKeyName(key))
}

func virtualKeyName(key uint32) string {
	if (key >= 'A' && key <= 'Z') || (key >= '0' && key <= '9') {
		return string(rune(key))
	}
	if key >= 0x70 && key <= 0x87 {
		return fmt.Sprintf("F%d", key-0x6f)
	}
	return fmt.Sprintf("VK 0x%02X", key)
}

func (app *application) startBackgroundDiagnostics() {
	app.tasks.Run(func(ctx context.Context, _ uint64) {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			snapshot := win32.SnapshotResources()
			select {
			case app.snapshots <- snapshot:
			default:
			}
			win32.PostMessage(app.hwnd, messageSnapshot, 0, 0)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

func (app *application) startSmokeHooks() {
	if ready := os.Getenv("GENSHINTOOLS_S02_READY_FILE"); ready != "" {
		_ = os.WriteFile(ready, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	if value := os.Getenv("GENSHINTOOLS_S02_AUTOCLOSE_MS"); value != "" {
		milliseconds, err := strconv.Atoi(value)
		if err == nil && milliseconds > 0 {
			app.tasks.Run(func(ctx context.Context, _ uint64) {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(milliseconds) * time.Millisecond):
					win32.PostMessage(app.hwnd, win32.WM_CLOSE, 0, 0)
				}
			})
		}
	}
}
