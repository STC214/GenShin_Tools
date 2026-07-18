// Package shell implements the stable S02 Windows shell and lifetime boundary.
package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"genshintools/internal/buildinfo"
	"genshintools/internal/capture"
	"genshintools/internal/config"
	"genshintools/internal/diagnostics"
	"genshintools/internal/game"
	"genshintools/internal/gamewindow"
	"genshintools/internal/injection"
	"genshintools/internal/input"
	"genshintools/internal/launch"
	"genshintools/internal/localenhance"
	"genshintools/internal/overlay"
	"genshintools/internal/paths"
	"genshintools/internal/platform/win32"
	"genshintools/internal/plugins"
	"genshintools/internal/resources"
	"genshintools/internal/taskrunner"
)

const (
	windowClass  = "GenshinTools.MainWindow.S02"
	instanceName = "Local\\GenshinTools.Singleton.S02"

	messageActivate     = win32.WM_APP + 1
	messageTray         = win32.WM_APP + 2
	messageSnapshot     = win32.WM_APP + 3
	messageInput        = win32.WM_APP + 4
	messagePhysical     = win32.WM_APP + 5
	messageGame         = win32.WM_APP + 6
	messageLaunch       = win32.WM_APP + 7
	messageResource     = win32.WM_APP + 8
	messageServer       = win32.WM_APP + 9
	messageCapture      = win32.WM_APP + 10
	messageOverlay      = win32.WM_APP + 11
	messageOverlayStats = win32.WM_APP + 12
	messageInjection    = win32.WM_APP + 13
	messagePlugins      = win32.WM_APP + 14

	trayID          = 1
	captureHotkeyID = 2001
	menuShow        = 1001
	menuExit        = 1002
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

	snapshots               chan win32.ResourceSnapshot
	lastSnap                win32.ResourceSnapshot
	inputNative             *input.Native
	inputUpdates            chan input.Snapshot
	physicalEvents          chan input.PhysicalEvent
	inputSnap               input.Snapshot
	recording               int
	inputUIError            string
	gameUpdates             chan gameUpdate
	gameState               gameViewState
	gameTask                uint64
	launchEngine            *launch.Engine
	launchUpdates           chan launch.Snapshot
	launchSnap              launch.Snapshot
	launchUIError           string
	shortcutStatus          string
	resourceUpdates         chan resourceUpdate
	resourceState           resourceViewState
	resourceTask            uint64
	localStatus             string
	betterGITask            uint64
	serverUpdates           chan serverUpdate
	serverState             serverViewState
	serverTask              uint64
	captureManager          *capture.Manager
	captureResults          chan capture.Result
	captureStatus           string
	captureHotkeyRegistered bool
	overlaySession          *overlay.Session
	overlayUpdates          chan overlayUpdate
	overlayStatsUpdates     chan overlay.Stats
	overlayStats            overlay.Stats
	overlayStatus           string
	mediaTarget             gamewindow.Target
	mediaTask               uint64
	injectionUpdates        chan injectionUpdate
	injectionModules        []injection.Audit
	injectionWarnings       []string
	injectionStatus         string
	injectionAuditTask      uint64
	injectionLaunchTask     uint64
	injectionLaunching      bool
	pluginLayout            plugins.Layout
	pluginState             plugins.State
	pluginItems             []plugins.Item
	pluginWarnings          []string
	pluginStatus            string
	pluginSelected          string
	pluginUpdates           chan pluginUpdate
	pluginTask              uint64
	pluginBusy              bool
	pluginDeleteConfirm     string
	pluginCatalog           plugins.Catalog
	pluginCatalogPage       plugins.CatalogPage
	customArgumentsEdit     win32.HWND
	pluginAliasEdit         win32.HWND
	pluginCatalogEdit       win32.HWND
	pluginSearchEdit        win32.HWND
	editBrush               win32.HBRUSH
	sessionNotifications    bool
	shutdown                sync.Once
	cleanExit               bool
	fatal                   bool
}

type gameViewState struct {
	Scanning       bool
	Candidate      *game.Candidate
	CandidateCount int
	Size           game.SizeProgress
	Skipped        uint64
	Running        []game.ProcessIdentity
	Status         string
	Error          string
}

type gameUpdate struct {
	taskID uint64
	state  gameViewState
}

type resourceViewState struct {
	Busy        bool
	Confirm     bool
	Language    string
	Status      string
	Error       string
	Version     string
	Manifest    resources.Manifest
	Plan        resources.RepairPlan
	Progress    resources.Progress
	HasPlan     bool
	PreDownload bool
}

type resourceUpdate struct {
	taskID  uint64
	state   resourceViewState
	refresh bool
}

type serverViewState struct {
	Busy           bool
	Confirm        bool
	Advanced       bool
	Target         localenhance.QuickServer
	AdvancedTarget localenhance.AdvancedServer
	Status         string
	Error          string
	Plan           resources.RepairPlan
	Transaction    *resources.Transaction
}

type serverUpdate struct {
	taskID  uint64
	state   serverViewState
	refresh bool
}

type overlayUpdate struct {
	taskID  uint64
	target  gamewindow.Target
	session *overlay.Session
	err     error
}

type injectionUpdate struct {
	taskID   uint64
	kind     uint8
	modules  []injection.Audit
	warnings []string
	status   string
	err      string
}

type pluginUpdate struct {
	taskID  uint64
	state   *plugins.State
	catalog *plugins.Catalog
	page    plugins.CatalogPage
	status  string
	err     string
}

var navigation = []struct{ title, subtitle string }{
	{"首页", "启动状态与常用操作将在后续阶段接入。"},
	{"游戏管理", "S05：只读状态、纯净启动和启动设置。"},
	{"资源管理", "S06：检查、下载、校验和事务修复。"},
	{"区服工具", "S07：官服/B服快速切换与高级服务器转换。"},
	{"本地增强", "S07：HDR、启动声音、BetterGI 与可回滚区服操作。"},
	{"截图与性能", "S08：游戏窗口截图与 FPS/CPU/GPU 覆盖层。"},
	{"输入增强", "S03：鼠标连点与键盘连按。"},
	{"注入适配", "S09：独立 helper、模块预检与纯净启动回退。"},
	{"插件", "S10：插件发现、配置、来源审计和更新。"},
	{"插件商店", "S10：显式 HTTPS 目录、搜索分类、分页和事务安装。"},
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
	pluginLayout, err := plugins.NewLayout(layout.Data, layout.Modules)
	if err != nil {
		return fmt.Errorf("create plugin layout: %w", err)
	}
	if err := pluginLayout.Ensure(); err != nil {
		return fmt.Errorf("ensure plugin layout: %w", err)
	}
	pluginLoad, err := plugins.LoadState(pluginLayout.State)
	if err != nil {
		return fmt.Errorf("load plugin state: %w", err)
	}
	if err := plugins.RecoverTransaction(pluginLayout, &pluginLoad.State); err != nil {
		return fmt.Errorf("recover plugin transaction: %w", err)
	}
	pluginItems, pluginWarnings, err := plugins.Discover(layout.Modules, pluginLoad.State)
	if err != nil {
		return fmt.Errorf("discover plugins: %w", err)
	}
	pluginSelected := ""
	if len(pluginItems) > 0 {
		pluginSelected = pluginItems[0].Manifest.ID
	}
	pluginCatalog := plugins.Catalog{}
	pluginCatalogPage := plugins.CatalogPage{}
	pluginStatus := fmt.Sprintf("已发现 %d 个本地插件；目录源默认关闭", len(pluginItems))
	if loaded.Settings.Plugins.CatalogURL != "" {
		cached, cacheErr := plugins.LoadCatalogForSource(pluginLayout.Catalog, loaded.Settings.Plugins.CatalogURL)
		if cacheErr == nil {
			if page, queryErr := plugins.QueryCatalog(cached, loaded.Settings.Plugins); queryErr == nil {
				pluginCatalog, pluginCatalogPage = cached, page
				pluginStatus = fmt.Sprintf("已载入目录缓存：%d 项；重新扫描可联网同步", page.Total)
			}
		} else if !errors.Is(cacheErr, os.ErrNotExist) {
			pluginStatus = "插件目录缓存无效，未影响本地插件"
		}
	}
	app := &application{
		instance:            win32.ModuleHandle(),
		settings:            loaded.Settings,
		lastBounds:          loaded.Settings.Window,
		layout:              layout,
		build:               build,
		logger:              logger,
		tasks:               taskrunner.New(),
		previousBad:         previousBad,
		recovered:           loaded.RecoveredFrom,
		snapshots:           make(chan win32.ResourceSnapshot, 1),
		inputUpdates:        make(chan input.Snapshot, 1),
		physicalEvents:      make(chan input.PhysicalEvent, 16),
		gameUpdates:         make(chan gameUpdate, 1),
		launchUpdates:       make(chan launch.Snapshot, 1),
		resourceUpdates:     make(chan resourceUpdate, 1),
		resourceState:       resourceViewState{Language: "zh-cn", Status: "先检查在线资源并生成只读修复计划"},
		serverUpdates:       make(chan serverUpdate, 1),
		serverState:         serverViewState{Target: localenhance.QuickOfficial, AdvancedTarget: localenhance.AdvancedGlobal, Status: "先生成区服变更预览；不会直接修改游戏目录"},
		captureResults:      make(chan capture.Result, 2),
		overlayUpdates:      make(chan overlayUpdate, 4),
		overlayStatsUpdates: make(chan overlay.Stats, 1),
		injectionUpdates:    make(chan injectionUpdate, 2),
		pluginLayout:        pluginLayout,
		pluginState:         pluginLoad.State,
		pluginItems:         pluginItems,
		pluginWarnings:      pluginWarnings,
		pluginSelected:      pluginSelected,
		pluginStatus:        pluginStatus,
		pluginCatalog:       pluginCatalog,
		pluginCatalogPage:   pluginCatalogPage,
		pluginUpdates:       make(chan pluginUpdate, 1),
		captureStatus:       "截图功能未启用",
		overlayStatus:       "性能覆盖层未启用",
		injectionStatus:     "注入默认关闭；纯净启动始终可用",
	}
	active = app
	defer func() { active = nil }()

	logger.Info("application starting", map[string]any{"version": build.Version, "commit": build.Commit, "previousUncleanExit": previousBad})
	if loaded.RecoveredFrom != "" {
		logger.Error("corrupt settings quarantined", map[string]any{"path": loaded.RecoveredFrom})
	}
	if pluginLoad.RecoveredFrom != "" {
		logger.Error("corrupt plugin state quarantined", map[string]any{"path": pluginLoad.RecoveredFrom})
		app.pluginStatus = "损坏的插件状态已隔离，已恢复安全默认值"
	}
	if err := resources.RecoverTransactions(layout.Staging); err != nil {
		logger.Error("resource transaction recovery", map[string]any{"error": err.Error()})
		app.resourceState.Error = "检测到未能自动恢复的资源事务，请查看日志"
	} else {
		logger.Info("resource transaction recovery complete", nil)
	}

	if err := app.createWindow(); err != nil {
		return err
	}
	if app.settings.LocalEnhance.StartupSoundEnabled && app.settings.LocalEnhance.StartupSoundPath != "" {
		if err := localenhance.PlayStartupSound(app.settings.LocalEnhance.StartupSoundPath); err != nil {
			logger.Error("play startup sound", map[string]any{"error": err.Error()})
		}
	}
	if err := app.startLauncher(); err != nil {
		app.requestShutdown()
		return fmt.Errorf("start pure launcher: %w", err)
	}
	if err := app.startInput(); err != nil {
		app.requestShutdown()
		return fmt.Errorf("start input enhancement: %w", err)
	}
	if err := app.startCaptureOverlay(); err != nil {
		app.requestShutdown()
		return fmt.Errorf("start capture and overlay: %w", err)
	}
	app.startBackgroundDiagnostics()
	app.startGameScan(os.Getenv("GENSHINTOOLS_S04_GAME_PATH"))
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
	if err := app.createLaunchControls(); err != nil {
		win32.DestroyWindow(hwnd)
		return err
	}
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
	case messageGame:
		for {
			select {
			case update := <-app.gameUpdates:
				if update.taskID == app.gameTask {
					app.gameState = update.state
					app.reconcileCaptureOverlay(false)
				}
			default:
				win32.Invalidate(hwnd)
				return 0
			}
		}
	case messageLaunch:
		previous := app.launchSnap.State
		for {
			select {
			case app.launchSnap = <-app.launchUpdates:
			default:
				if previous != launch.StateRunning && app.launchSnap.State == launch.StateRunning {
					if app.injectionLaunching {
						app.injectionStatus = fmt.Sprintf("注入启动成功：PID %d；模块已由 helper 核验", app.launchSnap.PID)
						app.injectionLaunching = false
					}
					app.applyPostLaunch(app.launchSnap.PostBehavior)
					app.scheduleBetterGI()
				}
				win32.Invalidate(hwnd)
				return 0
			}
		}
	case messageResource:
		for {
			select {
			case update := <-app.resourceUpdates:
				if update.taskID == app.resourceTask {
					app.resourceState = update.state
					if update.refresh && app.gameState.Candidate != nil {
						app.startGameScan(app.gameState.Candidate.Root)
					}
				}
			default:
				win32.Invalidate(hwnd)
				return 0
			}
		}
	case messageServer:
		for {
			select {
			case update := <-app.serverUpdates:
				if update.taskID == app.serverTask {
					app.serverState = update.state
					if update.refresh && app.gameState.Candidate != nil {
						app.startGameScan(app.gameState.Candidate.Root)
					}
				}
			default:
				win32.Invalidate(hwnd)
				return 0
			}
		}
	case messageCapture:
		for {
			select {
			case result := <-app.captureResults:
				if result.Error != "" {
					app.captureStatus = "截图失败：" + result.Error
					app.logger.Error("capture game window", map[string]any{"error": result.Error})
				} else {
					app.captureStatus = "截图已保存：" + result.Path
					app.logger.Info("game screenshot saved", map[string]any{"path": result.Path})
				}
			default:
				win32.Invalidate(hwnd)
				return 0
			}
		}
	case messageOverlay:
		for {
			select {
			case update := <-app.overlayUpdates:
				if update.taskID == app.mediaTask {
					app.overlaySession = update.session
					if update.err != nil {
						app.overlayStatus = "覆盖层启动失败：" + update.err.Error()
					} else if update.session != nil {
						app.overlayStatus = fmt.Sprintf("覆盖层运行中：PID %d", update.target.PID)
					} else {
						app.overlayStatus = "性能覆盖层已停止"
					}
				} else if update.session != nil {
					app.tasks.Run(func(ctx context.Context, _ uint64) { _ = update.session.Close(ctx) })
				}
			default:
				win32.Invalidate(hwnd)
				return 0
			}
		}
	case messageOverlayStats:
		for {
			select {
			case app.overlayStats = <-app.overlayStatsUpdates:
			default:
				win32.Invalidate(hwnd)
				return 0
			}
		}
	case messageInjection:
		for {
			select {
			case update := <-app.injectionUpdates:
				valid := (update.kind == 0 && update.taskID == app.injectionAuditTask) || (update.kind == 1 && update.taskID == app.injectionLaunchTask)
				if valid {
					if update.kind == 0 {
						app.injectionModules = update.modules
						app.injectionWarnings = update.warnings
						selected := false
						for _, module := range update.modules {
							selected = selected || module.Manifest.ID == app.settings.Injection.ModuleID
						}
						if !selected {
							app.settings.Injection.ModuleID = ""
							if len(update.modules) > 0 {
								app.settings.Injection.ModuleID = update.modules[0].Manifest.ID
							}
							app.saveInjectionSettings()
						}
					}
					if update.err != "" {
						app.injectionStatus = update.err
						app.injectionLaunching = false
					} else if update.status != "" {
						app.injectionStatus = update.status
					}
				}
			default:
				win32.Invalidate(hwnd)
				return 0
			}
		}
	case messagePlugins:
		for {
			select {
			case update := <-app.pluginUpdates:
				if update.taskID == app.pluginTask {
					app.pluginBusy = false
					if update.err != "" {
						app.pluginStatus = update.err
					} else {
						if update.state != nil {
							app.pluginState = *update.state
						}
						if update.catalog != nil {
							app.pluginCatalog = *update.catalog
							app.pluginCatalogPage = update.page
						}
						app.refreshPlugins()
						app.pluginStatus = update.status
					}
				}
			default:
				win32.Invalidate(hwnd)
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
			app.updateLaunchControlVisibility()
			win32.Invalidate(hwnd)
		} else if app.selected == 1 && x >= int(win32.Scale(210, app.dpi)) {
			app.gameClick(x, y)
		} else if app.selected == 2 && x >= int(win32.Scale(210, app.dpi)) {
			app.resourceClick(x, y)
		} else if app.selected == 3 && x >= int(win32.Scale(210, app.dpi)) {
			app.serverClick(x, y)
		} else if app.selected == 4 && x >= int(win32.Scale(210, app.dpi)) {
			app.localEnhanceClick(x, y)
		} else if app.selected == 5 && x >= int(win32.Scale(210, app.dpi)) {
			app.mediaClick(x, y)
		} else if app.selected == 6 && x >= int(win32.Scale(210, app.dpi)) {
			app.inputClick(x, y)
		} else if app.selected == 7 && x >= int(win32.Scale(210, app.dpi)) {
			app.injectionClick(y)
		} else if app.selected == 8 && x >= int(win32.Scale(210, app.dpi)) {
			app.pluginClick(x, y)
		} else if app.selected == 9 && x >= int(win32.Scale(210, app.dpi)) {
			app.pluginStoreClick(x, y)
		}
		return 0
	case win32.WM_HOTKEY:
		if int32(wParam) == captureHotkeyID && app.captureManager != nil {
			if app.captureManager.Request() {
				app.captureStatus = "截图请求已进入有界队列…"
			} else {
				app.captureStatus = "截图请求未接受：无已核验游戏窗口或队列已满"
			}
			win32.Invalidate(hwnd)
		}
		return 0
	case win32.WM_KEYDOWN:
		switch wParam {
		case win32.VK_UP:
			if app.selected > 0 {
				app.selected--
				app.updateLaunchControlVisibility()
				win32.Invalidate(hwnd)
			}
		case win32.VK_DOWN:
			if app.selected < len(navigation)-1 {
				app.selected++
				app.updateLaunchControlVisibility()
				win32.Invalidate(hwnd)
			}
		}
		return 0
	case win32.WM_COMMAND:
		controlID := uint16(wParam & 0xffff)
		notification := uint16((wParam >> 16) & 0xffff)
		if controlID == 2003 && notification == 0x0200 {
			app.savePluginCatalogURL()
			win32.Invalidate(hwnd)
		} else if controlID == 2004 && notification == 0x0200 {
			app.savePluginSearch()
			win32.Invalidate(hwnd)
		}
		return 0
	case win32.WM_CTLCOLOREDIT:
		win32.SetTextColor(win32.HDC(wParam), win32.Color(225, 229, 242))
		win32.SetBackgroundColor(win32.HDC(wParam), win32.Color(25, 29, 39))
		return uintptr(app.editBrush)
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
			app.layoutLaunchControls()
			win32.Invalidate(hwnd)
		}
		return 0
	case win32.WM_DPICHANGED:
		app.dpi = uint32(wParam & 0xffff)
		app.recreateFonts()
		app.layoutLaunchControls()
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
		localenhance.StopStartupSound()
		if app.captureHotkeyRegistered {
			win32.UnregisterHotKey(app.hwnd, captureHotkeyID)
			app.captureHotkeyRegistered = false
		}
		if app.captureManager != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := app.captureManager.Close(ctx); err != nil {
				app.logger.Error("screenshot worker shutdown", map[string]any{"error": err.Error()})
			}
			cancel()
			app.captureManager = nil
		}
		app.tasks.Cancel(app.mediaTask)
		app.tasks.Cancel(app.injectionAuditTask)
		app.tasks.Cancel(app.injectionLaunchTask)
		app.tasks.Cancel(app.pluginTask)
		if app.overlaySession != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := app.overlaySession.Close(ctx); err != nil {
				app.logger.Error("overlay session shutdown", map[string]any{"error": err.Error()})
			}
			cancel()
			app.overlaySession = nil
		}
		if !app.serverState.Busy && app.serverState.Transaction != nil {
			_ = app.serverState.Transaction.Abort()
			app.serverState.Transaction = nil
		}
		app.syncLaunchConfig()
		app.captureBounds()
		app.settings.Window = app.lastBounds
		if err := config.Save(app.layout.Config, app.settings); err != nil {
			app.logger.Error("save settings", map[string]any{"error": err.Error()})
		}
		if app.trayAdded {
			win32.DeleteTrayIcon(&app.tray)
			app.trayAdded = false
		}
		if !app.tasks.Shutdown(2 * time.Second) {
			app.logger.Error("background task shutdown timed out", nil)
		}
		if app.inputNative != nil {
			app.inputNative.Close()
			app.inputNative = nil
		}
		if app.launchEngine != nil {
			app.launchEngine.Close()
			app.launchEngine = nil
		}
		if app.sessionNotifications {
			win32.UnregisterSessionNotifications(app.hwnd)
			app.sessionNotifications = false
		}
		app.deleteFonts()
		win32.DeleteObject(uintptr(app.editBrush))
		app.editBrush = 0
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
	if app.customArgumentsEdit != 0 {
		win32.SetControlFont(app.customArgumentsEdit, app.fontBody)
	}
	if app.pluginAliasEdit != 0 {
		win32.SetControlFont(app.pluginAliasEdit, app.fontBody)
	}
	if app.pluginCatalogEdit != 0 {
		win32.SetControlFont(app.pluginCatalogEdit, app.fontBody)
	}
	if app.pluginSearchEdit != 0 {
		win32.SetControlFont(app.pluginSearchEdit, app.fontBody)
	}
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

	if app.selected == 1 {
		app.paintGame(dc, client, contentLeft)
	} else if app.selected == 2 {
		app.paintResources(dc, client, contentLeft)
	} else if app.selected == 3 {
		app.paintServer(dc, client, contentLeft)
	} else if app.selected == 4 {
		app.paintLocalEnhance(dc, client, contentLeft)
	} else if app.selected == 5 {
		app.paintMedia(dc, client, contentLeft)
	} else if app.selected == 6 {
		app.paintInput(dc, client, contentLeft)
	} else if app.selected == 7 {
		app.paintInjection(dc, client, contentLeft)
	} else if app.selected == 8 {
		app.paintPlugins(dc, client, contentLeft)
	} else if app.selected == 9 {
		app.paintPluginStore(dc, client, contentLeft)
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
	if app.selected == 8 || app.selected == 9 {
		statusText = app.pluginStatus
	}
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

func (app *application) startCaptureOverlay() error {
	if app.settings.Capture.SaveDir == "" {
		app.settings.Capture.SaveDir = filepath.Join("data", "screenshots")
	}
	app.captureManager = capture.NewManager(nil, func(result capture.Result) {
		select {
		case app.captureResults <- result:
		default:
			select {
			case <-app.captureResults:
			default:
			}
			app.captureResults <- result
		}
		win32.PostMessage(app.hwnd, messageCapture, 0, 0)
	})
	if err := app.captureManager.Configure(app.runtimeCaptureConfig()); err != nil {
		return err
	}
	if err := app.applyCaptureHotkey(); err != nil {
		app.captureStatus = "截图快捷键注册失败：" + err.Error()
		app.logger.Error("register screenshot hotkey", map[string]any{"error": err.Error()})
	} else if app.settings.Capture.Enabled {
		app.captureStatus = "截图已启用，等待已核验游戏窗口"
	}
	return nil
}

func (app *application) applyCaptureHotkey() error {
	if app.captureHotkeyRegistered {
		win32.UnregisterHotKey(app.hwnd, captureHotkeyID)
		app.captureHotkeyRegistered = false
	}
	if err := app.captureManager.Configure(app.runtimeCaptureConfig()); err != nil {
		return err
	}
	if !app.settings.Capture.Enabled {
		return nil
	}
	if app.settings.Capture.ConflictsWith(app.settings.Input.TriggerKey, app.settings.Input.OutputKey, app.settings.Input.StopKey) {
		return errors.New("截图键与输入增强物理键冲突")
	}
	if err := win32.RegisterHotKey(app.hwnd, captureHotkeyID, app.settings.Capture.Modifiers, app.settings.Capture.VirtualKey); err != nil {
		return err
	}
	app.captureHotkeyRegistered = true
	return nil
}

func (app *application) reconcileCaptureOverlay(force bool) {
	var target gamewindow.Target
	for _, process := range app.gameState.Running {
		if process.VerifiedPath && process.CreationTime != 0 {
			target = gamewindow.Target{PID: process.PID, CreationTime: process.CreationTime}
			break
		}
	}
	if app.captureManager != nil {
		if target.PID == 0 {
			app.captureManager.SetTarget(nil)
		} else {
			app.captureManager.SetTarget(&target)
		}
	}
	if app.overlaySession != nil {
		select {
		case <-app.overlaySession.Done():
			app.overlaySession = nil
		default:
		}
	}
	if !force && target == app.mediaTarget && ((app.settings.Overlay.Enabled && app.overlaySession != nil) || (!app.settings.Overlay.Enabled && app.overlaySession == nil)) {
		return
	}
	app.tasks.Cancel(app.mediaTask)
	old := app.overlaySession
	app.overlaySession = nil
	app.mediaTarget = target
	if app.settings.Overlay.Enabled && target.PID == 0 {
		app.overlayStatus = "覆盖层已启用，等待已核验游戏窗口"
	} else {
		app.overlayStatus = "正在协调覆盖层生命周期…"
	}
	configCopy := app.settings.Overlay
	app.mediaTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		if old != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = old.Close(closeCtx)
			cancel()
		}
		if ctx.Err() != nil || !configCopy.Enabled || target.PID == 0 {
			app.publishOverlay(overlayUpdate{taskID: id, target: target})
			return
		}
		session, err := overlay.Start(target, configCopy, func(stats overlay.Stats) {
			select {
			case app.overlayStatsUpdates <- stats:
			default:
				select {
				case <-app.overlayStatsUpdates:
				default:
				}
				app.overlayStatsUpdates <- stats
			}
			win32.PostMessage(app.hwnd, messageOverlayStats, 0, 0)
		})
		if ctx.Err() != nil && session != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = session.Close(closeCtx)
			cancel()
			session = nil
		}
		app.publishOverlay(overlayUpdate{taskID: id, target: target, session: session, err: err})
	})
}

func (app *application) publishOverlay(update overlayUpdate) {
	select {
	case app.overlayUpdates <- update:
		win32.PostMessage(app.hwnd, messageOverlay, 0, 0)
	default:
		if update.session != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = update.session.Close(closeCtx)
			cancel()
		}
	}
}

func (app *application) runtimeCaptureConfig() capture.Config {
	configured := app.settings.Capture
	if configured.SaveDir != "" && !filepath.IsAbs(configured.SaveDir) {
		configured.SaveDir = filepath.Join(app.layout.Root, configured.SaveDir)
	}
	return configured
}

func (app *application) portableCapturePath(selected string) string {
	relative, err := filepath.Rel(app.layout.Root, selected)
	if err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return relative
	}
	return selected
}

func (app *application) saveMediaSettings() bool {
	if err := config.Save(app.layout.Config, app.settings); err != nil {
		app.captureStatus = "保存 S08 设置失败：" + err.Error()
		return false
	}
	return true
}

func (app *application) mediaClick(_, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	switch {
	case y >= sy(170) && y < sy(214):
		app.settings.Capture.Enabled = !app.settings.Capture.Enabled
		if err := app.applyCaptureHotkey(); err != nil {
			app.captureStatus = "截图快捷键注册失败：" + err.Error()
		} else if app.settings.Capture.Enabled {
			app.captureStatus = "截图已启用：" + app.settings.Capture.HotkeyString()
		} else {
			app.captureStatus = "截图功能已停用"
		}
		app.saveMediaSettings()
	case y >= sy(220) && y < sy(264):
		presets := []uint32{0x79, 0x78, 0x2C}
		index := 0
		for i, key := range presets {
			if app.settings.Capture.VirtualKey == key {
				index = (i + 1) % len(presets)
				break
			}
		}
		for range presets {
			key := presets[index]
			index = (index + 1) % len(presets)
			if key != app.settings.Input.TriggerKey && key != app.settings.Input.OutputKey && key != app.settings.Input.StopKey {
				app.settings.Capture.VirtualKey = key
				break
			}
		}
		if err := app.applyCaptureHotkey(); err != nil {
			app.captureStatus = "切换截图键失败：" + err.Error()
		} else {
			app.captureStatus = "截图键已改为 " + app.settings.Capture.HotkeyString()
		}
		app.saveMediaSettings()
	case y >= sy(270) && y < sy(314):
		if app.captureManager != nil && app.captureManager.Request() {
			app.captureStatus = "手动截图请求已进入有界队列…"
		} else {
			app.captureStatus = "无法截图：请启用截图并启动已核验的游戏窗口"
		}
	case y >= sy(320) && y < sy(364):
		path, selected, err := win32.SelectFolder(app.hwnd, app.settings.Capture.SaveDir)
		if err != nil {
			app.captureStatus = "选择截图目录失败：" + err.Error()
		} else if selected {
			app.settings.Capture.SaveDir = app.portableCapturePath(path)
			if err := app.captureManager.Configure(app.runtimeCaptureConfig()); err != nil {
				app.captureStatus = "截图目录无效：" + err.Error()
			} else {
				app.captureStatus = "截图目录已设置：" + path
				app.saveMediaSettings()
			}
		}
	case y >= sy(370) && y < sy(414):
		app.settings.Overlay.Enabled = !app.settings.Overlay.Enabled
		app.saveMediaSettings()
		app.reconcileCaptureOverlay(true)
	case y >= sy(420) && y < sy(464):
		settings := &app.settings.Overlay
		switch {
		case settings.ShowFPS && settings.ShowCPU && settings.ShowGPU:
			settings.ShowCPU, settings.ShowGPU = false, false
		case settings.ShowFPS && !settings.ShowCPU && !settings.ShowGPU:
			settings.ShowFPS, settings.ShowCPU, settings.ShowGPU = false, true, true
		default:
			settings.ShowFPS, settings.ShowCPU, settings.ShowGPU = true, true, true
		}
		app.saveMediaSettings()
		app.reconcileCaptureOverlay(true)
	case y >= sy(470) && y < sy(514):
		presets := [][2]int{{16, 16}, {16, 120}, {300, 16}}
		index := 0
		for i, preset := range presets {
			if app.settings.Overlay.OffsetX == preset[0] && app.settings.Overlay.OffsetY == preset[1] {
				index = (i + 1) % len(presets)
				break
			}
		}
		app.settings.Overlay.OffsetX, app.settings.Overlay.OffsetY = presets[index][0], presets[index][1]
		app.saveMediaSettings()
		app.reconcileCaptureOverlay(true)
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) paintMedia(dc win32.HDC, client win32.Rect, left int32) {
	cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
	defer win32.DeleteObject(uintptr(cardBrush))
	buttonBrush := win32.CreateSolidBrush(win32.Color(35, 40, 54))
	defer win32.DeleteObject(uintptr(buttonBrush))
	activeBrush := win32.CreateSolidBrush(win32.Color(52, 66, 112))
	defer win32.DeleteObject(uintptr(activeBrush))
	right := client.Right - win32.Scale(42, app.dpi)
	row := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		win32.FillRect(dc, &rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	captureState := "停用"
	if app.settings.Capture.Enabled {
		captureState = "启用"
	}
	draw("游戏窗口截图："+captureState+"    单击切换", row(170, 214, activeBrush), win32.Color(235, 238, 248))
	draw("全局截图键："+app.settings.Capture.HotkeyString()+"    单击切换安全预设", row(220, 264, buttonBrush), win32.Color(225, 229, 242))
	draw("立即截取已核验游戏窗口", row(270, 314, buttonBrush), win32.Color(225, 229, 242))
	draw("保存目录："+app.settings.Capture.SaveDir, row(320, 364, buttonBrush), win32.Color(190, 197, 216))
	overlayState := "停用"
	if app.settings.Overlay.Enabled {
		overlayState = "启用"
	}
	draw("性能覆盖层："+overlayState+"    单击切换", row(370, 414, activeBrush), win32.Color(235, 238, 248))
	metrics := []string{}
	if app.settings.Overlay.ShowFPS {
		metrics = append(metrics, "FPS")
	}
	if app.settings.Overlay.ShowCPU {
		metrics = append(metrics, "CPU")
	}
	if app.settings.Overlay.ShowGPU {
		metrics = append(metrics, "GPU")
	}
	draw("显示指标："+strings.Join(metrics, " / ")+"    单击切换组合", row(420, 464, buttonBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf("覆盖层偏移：X %d · Y %d    单击切换预设", app.settings.Overlay.OffsetX, app.settings.Overlay.OffsetY), row(470, 514, buttonBrush), win32.Color(225, 229, 242))
	statsText := fmt.Sprintf("实时：FPS %s · CPU %s · GPU %s", metricValue(app.overlayStats.FPS, app.overlayStats.FPSValid), metricValue(app.overlayStats.CPU, app.overlayStats.CPUValid), metricValue(app.overlayStats.GPU, app.overlayStats.GPUValid))
	draw(statsText, row(526, 566, cardBrush), win32.Color(166, 174, 197))
	status := app.captureStatus + "  |  " + app.overlayStatus
	draw(status, row(572, 612, cardBrush), win32.Color(126, 136, 160))
}

func metricValue(value float64, valid bool) string {
	if !valid {
		return "N/A"
	}
	return fmt.Sprintf("%.1f", value)
}

func (app *application) saveInjectionSettings() bool {
	normalized, err := app.settings.Injection.Normalized()
	if err != nil {
		app.injectionStatus = "注入设置无效：" + err.Error()
		return false
	}
	app.settings.Injection = normalized
	if err := config.Save(app.layout.Config, app.settings); err != nil {
		app.injectionStatus = "保存注入设置失败：" + err.Error()
		return false
	}
	return true
}

func (app *application) publishInjection(update injectionUpdate) {
	select {
	case app.injectionUpdates <- update:
		win32.PostMessage(app.hwnd, messageInjection, 0, 0)
	default:
		select {
		case <-app.injectionUpdates:
		default:
		}
		select {
		case app.injectionUpdates <- update:
			win32.PostMessage(app.hwnd, messageInjection, 0, 0)
		default:
		}
	}
}

func (app *application) startInjectionAudit() {
	if app.gameState.Candidate == nil {
		app.injectionStatus = "请先在游戏管理页完成游戏路径与版本扫描"
		return
	}
	app.tasks.Cancel(app.injectionAuditTask)
	candidate := *app.gameState.Candidate
	app.injectionStatus = "正在后台核验模块 manifest、PE、版本与 SHA-256…"
	app.injectionAuditTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		modules, warnings, err := injection.DiscoverCompatible(app.layout.Modules, candidate)
		if ctx.Err() != nil {
			return
		}
		update := injectionUpdate{taskID: id, kind: 0, modules: modules, warnings: warnings}
		if err != nil {
			update.err = "模块审计失败：" + err.Error()
		} else {
			update.status = fmt.Sprintf("审计完成：%d 个兼容模块，%d 个拒绝/警告", len(modules), len(warnings))
		}
		app.publishInjection(update)
	})
}

func (app *application) startInjectionLaunch() {
	if app.gameState.Candidate == nil || app.launchEngine == nil || !app.syncLaunchConfig() {
		app.injectionStatus = "请先完成游戏扫描和启动设置校验"
		return
	}
	if !app.saveInjectionSettings() {
		return
	}
	settings := app.settings.Injection
	launchSettings := app.settings.Launch
	available := make(map[string]bool, len(app.pluginItems))
	for _, item := range app.pluginItems {
		available[item.Manifest.ID] = true
	}
	moduleIDs := plugins.EnabledInOrder(app.pluginState, available)
	if len(moduleIDs) > 0 && app.settings.Plugins.SafeMode {
		app.injectionStatus = "注入启动被插件安全模式阻止；请在插件页明确关闭安全模式"
		return
	}
	if !settings.Enabled || !settings.RiskAcknowledged || (settings.ModuleID == "" && len(moduleIDs) == 0) {
		app.injectionStatus = "注入启动被拒绝：请启用注入、确认风险并选择兼容模块"
		return
	}
	app.tasks.Cancel(app.injectionLaunchTask)
	candidate := *app.gameState.Candidate
	app.injectionStatus = "正在独立 helper 中重复预检并启动…"
	app.injectionLaunching = true
	app.injectionLaunchTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		starter := injection.Starter{Context: ctx, HelperPath: filepath.Join(app.layout.Root, "GenshinTools-injector.exe"), ModulesRoot: app.layout.Modules, StagingRoot: app.layout.Staging, Config: settings, ModuleIDs: moduleIDs}
		err := app.launchEngine.LaunchWithStarter(candidate, launchSettings, starter)
		update := injectionUpdate{taskID: id, kind: 1, status: "helper 已完成，正在接管游戏进程观察"}
		if err != nil {
			update.status = ""
			update.err = "注入启动失败：" + err.Error() + "；可立即使用纯净启动"
		}
		app.publishInjection(update)
	})
}

func (app *application) injectionClick(y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	switch {
	case y >= sy(170) && y < sy(214):
		app.settings.Injection.Enabled = !app.settings.Injection.Enabled
		app.saveInjectionSettings()
	case y >= sy(220) && y < sy(264):
		app.settings.Injection.RiskAcknowledged = !app.settings.Injection.RiskAcknowledged
		app.saveInjectionSettings()
	case y >= sy(270) && y < sy(314):
		if len(app.injectionModules) == 0 {
			app.startInjectionAudit()
			break
		}
		index := 0
		for i, module := range app.injectionModules {
			if module.Manifest.ID == app.settings.Injection.ModuleID {
				index = (i + 1) % len(app.injectionModules)
				break
			}
		}
		app.settings.Injection.ModuleID = app.injectionModules[index].Manifest.ID
		app.injectionStatus = "已选择模块：" + app.injectionModules[index].Manifest.Name
		app.saveInjectionSettings()
	case y >= sy(320) && y < sy(364):
		app.startInjectionAudit()
	case y >= sy(370) && y < sy(414):
		app.startInjectionLaunch()
	case y >= sy(420) && y < sy(464):
		app.injectionLaunching = false
		if app.gameState.Candidate == nil || app.launchEngine == nil || !app.syncLaunchConfig() {
			app.injectionStatus = "请先完成游戏扫描"
		} else if err := app.launchEngine.Launch(*app.gameState.Candidate, app.settings.Launch); err != nil {
			app.injectionStatus = "纯净启动失败：" + err.Error()
		} else {
			app.injectionStatus = "已使用 S05 纯净启动路径"
		}
	case y >= sy(470) && y < sy(514):
		presets := [][2]int{{15000, 5000}, {30000, 10000}, {60000, 20000}}
		index := 0
		for i, preset := range presets {
			if preset[0] == app.settings.Injection.HelperTimeoutMS && preset[1] == app.settings.Injection.RemoteTimeoutMS {
				index = (i + 1) % len(presets)
				break
			}
		}
		app.settings.Injection.HelperTimeoutMS, app.settings.Injection.RemoteTimeoutMS = presets[index][0], presets[index][1]
		app.saveInjectionSettings()
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) paintInjection(dc win32.HDC, client win32.Rect, left int32) {
	cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
	defer win32.DeleteObject(uintptr(cardBrush))
	buttonBrush := win32.CreateSolidBrush(win32.Color(35, 40, 54))
	defer win32.DeleteObject(uintptr(buttonBrush))
	warningBrush := win32.CreateSolidBrush(win32.Color(74, 48, 35))
	defer win32.DeleteObject(uintptr(warningBrush))
	accentBrush := win32.CreateSolidBrush(win32.Color(52, 66, 112))
	defer win32.DeleteObject(uintptr(accentBrush))
	right := client.Right - win32.Scale(42, app.dpi)
	row := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		win32.FillRect(dc, &rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	settings := app.settings.Injection
	draw("注入适配："+map[bool]string{true: "启用", false: "关闭"}[settings.Enabled]+"    默认关闭，单击切换", row(170, 214, accentBrush), win32.Color(235, 238, 248))
	draw("风险确认："+map[bool]string{true: "已确认", false: "未确认"}[settings.RiskAcknowledged]+"    可能触发反作弊、崩溃或账号风险", row(220, 264, warningBrush), win32.Color(255, 205, 150))
	module := "无兼容模块"
	for _, audit := range app.injectionModules {
		if audit.Manifest.ID == settings.ModuleID {
			module = audit.Manifest.Name + " · " + audit.SHA256[:12]
			break
		}
	}
	draw("模块："+module+"    单击切换", row(270, 314, buttonBrush), win32.Color(225, 229, 242))
	draw("重新审计 data\\injection\\modules（不会联网下载）", row(320, 364, buttonBrush), win32.Color(190, 197, 216))
	helperMode := map[bool]string{true: "管理员", false: "当前用户"}[settings.ElevatedHelper]
	draw("使用独立 "+helperMode+" helper 注入启动", row(370, 414, warningBrush), win32.Color(255, 205, 150))
	draw("立即纯净启动（与 S05 完全相同）", row(420, 464, accentBrush), win32.Color(235, 238, 248))
	draw(fmt.Sprintf("超时：helper %.0f 秒 · 远程加载 %.0f 秒    单击切换", float64(settings.HelperTimeoutMS)/1000, float64(settings.RemoteTimeoutMS)/1000), row(470, 514, buttonBrush), win32.Color(190, 197, 216))
	warning := "无模块审计警告"
	if len(app.injectionWarnings) > 0 {
		warning = app.injectionWarnings[0]
	}
	draw(warning, row(526, 566, cardBrush), win32.Color(166, 174, 197))
	draw(app.injectionStatus, row(572, 612, cardBrush), win32.Color(145, 154, 180))
}

func (app *application) savePluginSettings() bool {
	normalized, err := app.settings.Plugins.Normalized()
	if err != nil {
		app.pluginStatus = "插件设置无效：" + err.Error()
		return false
	}
	app.settings.Plugins = normalized
	if err := config.Save(app.layout.Config, app.settings); err != nil {
		app.pluginStatus = "保存插件设置失败：" + err.Error()
		return false
	}
	return true
}

func (app *application) refreshPlugins() bool {
	items, warnings, err := plugins.Discover(app.layout.Modules, app.pluginState)
	if err != nil {
		app.pluginStatus = "发现插件失败：" + err.Error()
		return false
	}
	app.pluginItems, app.pluginWarnings = items, warnings
	found := false
	for _, item := range items {
		found = found || item.Manifest.ID == app.pluginSelected
	}
	if !found {
		app.pluginSelected = ""
		if len(items) > 0 {
			app.pluginSelected = items[0].Manifest.ID
		}
	}
	app.pluginStatus = fmt.Sprintf("已发现 %d 个插件，%d 条警告", len(items), len(warnings))
	return true
}

func (app *application) selectedPlugin() (plugins.Item, bool) {
	for _, item := range app.pluginItems {
		if item.Manifest.ID == app.pluginSelected {
			return item, true
		}
	}
	return plugins.Item{}, false
}

func (app *application) pluginClick(x, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	switch {
	case y >= sy(170) && y < sy(214):
		app.settings.Plugins.SafeMode = !app.settings.Plugins.SafeMode
		if app.savePluginSettings() {
			app.pluginStatus = map[bool]string{true: "安全模式已开启：插件不会注入", false: "安全模式已关闭：仅启用插件会按顺序注入"}[app.settings.Plugins.SafeMode]
		}
	case y >= sy(220) && y < sy(264):
		app.refreshPlugins()
		if app.settings.Plugins.CatalogURL != "" {
			app.startPluginCatalogSync()
		}
	case y >= sy(270) && y < sy(314):
		if len(app.pluginItems) == 0 {
			app.pluginStatus = "没有已安装插件"
			break
		}
		index := 0
		for i, item := range app.pluginItems {
			if item.Manifest.ID == app.pluginSelected {
				index = (i + 1) % len(app.pluginItems)
				break
			}
		}
		app.pluginSelected = app.pluginItems[index].Manifest.ID
		app.pluginDeleteConfirm = ""
		app.syncPluginAliasEdit()
		app.pluginStatus = "已选择：" + app.pluginItems[index].DisplayName()
	case y >= sy(320) && y < sy(364):
		item, ok := app.selectedPlugin()
		if !ok {
			app.pluginStatus = "请先选择插件"
			break
		}
		next := plugins.CloneState(app.pluginState)
		if err := plugins.SetEnabled(&next, item.Manifest.ID, !item.Enabled); err != nil {
			app.pluginStatus = "切换插件失败：" + err.Error()
		} else if err := plugins.SaveState(app.pluginLayout.State, next); err != nil {
			app.pluginStatus = "保存插件状态失败：" + err.Error()
		} else {
			app.pluginState = next
			app.refreshPlugins()
		}
	case y >= sy(370) && y < sy(414):
		// The child edit control owns this row.
	case y >= sy(420) && y < sy(464):
		client := win32.GetClientRect(app.hwnd)
		left := int(win32.Scale(252, app.dpi))
		width := int(client.Right) - left - int(win32.Scale(42, app.dpi))
		third := max(1, width/3)
		switch {
		case x < left+third:
			app.savePluginAlias()
		case x < left+2*third:
			app.movePlugin(-1)
		default:
			app.movePlugin(1)
		}
	case y >= sy(470) && y < sy(514):
		app.startLocalPluginInstall()
	case y >= sy(520) && y < sy(564):
		client := win32.GetClientRect(app.hwnd)
		left := int(win32.Scale(252, app.dpi))
		width := int(client.Right) - left - int(win32.Scale(42, app.dpi))
		third := max(1, width/3)
		if x < left+third {
			app.startPluginRollback()
		} else if x < left+2*third {
			app.startPluginUninstall()
		} else {
			app.applyNextPluginPreset()
		}
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) savePluginAlias() {
	item, ok := app.selectedPlugin()
	if !ok {
		app.pluginStatus = "请先选择插件"
		return
	}
	alias := win32.GetWindowText(app.pluginAliasEdit)
	next := plugins.CloneState(app.pluginState)
	if err := plugins.SetAlias(&next, item.Manifest.ID, alias); err != nil {
		app.pluginStatus = "插件别名无效：" + err.Error()
		return
	}
	if err := plugins.SaveState(app.pluginLayout.State, next); err != nil {
		app.pluginStatus = "保存插件别名失败：" + err.Error()
		return
	}
	app.pluginState = next
	app.refreshPlugins()
	app.pluginStatus = "插件别名已保存"
}

func (app *application) savePluginCatalogURL() {
	if app.pluginCatalogEdit == 0 {
		return
	}
	next := app.settings.Plugins
	next.CatalogURL = strings.TrimSpace(win32.GetWindowText(app.pluginCatalogEdit))
	normalized, err := next.Normalized()
	if err != nil {
		app.pluginStatus = "插件目录 URL 无效：" + err.Error()
		win32.SetWindowText(app.pluginCatalogEdit, app.settings.Plugins.CatalogURL)
		return
	}
	settings := app.settings
	settings.Plugins = normalized
	if err := config.Save(app.layout.Config, settings); err != nil {
		app.pluginStatus = "保存插件目录 URL 失败：" + err.Error()
		win32.SetWindowText(app.pluginCatalogEdit, app.settings.Plugins.CatalogURL)
		return
	}
	app.settings = settings
	if normalized.CatalogURL == "" {
		app.pluginCatalog = plugins.Catalog{}
		app.pluginCatalogPage = plugins.CatalogPage{}
		app.pluginStatus = "插件目录已关闭；不会发起目录网络请求"
	} else {
		app.pluginStatus = "插件目录 URL 已保存；单击重新扫描以同步"
	}
}

func (app *application) startPluginCatalogSync() {
	if app.pluginBusy || app.settings.Plugins.CatalogURL == "" {
		return
	}
	url := app.settings.Plugins.CatalogURL
	configCopy := app.settings.Plugins
	destination := app.pluginLayout.Catalog
	app.pluginBusy = true
	app.pluginStatus = "正在同步并校验插件目录"
	app.pluginTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		catalog, err := plugins.SyncCatalog(ctx, nil, url, destination)
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: "插件目录同步失败，保留原缓存：" + err.Error()})
			return
		}
		page, err := plugins.QueryCatalog(catalog, configCopy)
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: "插件目录查询失败：" + err.Error()})
			return
		}
		app.publishPlugin(pluginUpdate{taskID: id, catalog: &catalog, page: page, status: fmt.Sprintf("目录同步完成：共 %d 项，当前第 %d/%d 页", page.Total, page.Page, page.TotalPages)})
	})
}

func (app *application) savePluginSearch() {
	if app.pluginSearchEdit == 0 {
		return
	}
	next := app.settings.Plugins
	next.Search = strings.TrimSpace(win32.GetWindowText(app.pluginSearchEdit))
	next.Page = 1
	app.applyPluginStoreConfig(next)
}

func (app *application) applyPluginStoreConfig(next plugins.Config) {
	normalized, err := next.Normalized()
	if err != nil {
		app.pluginStatus = "插件商店筛选无效：" + err.Error()
		return
	}
	if app.pluginCatalog.SchemaVersion != 0 {
		page, err := plugins.QueryCatalog(app.pluginCatalog, normalized)
		if err != nil {
			app.pluginStatus = "筛选插件目录失败：" + err.Error()
			return
		}
		app.pluginCatalogPage = page
		normalized.Page = page.Page
	}
	settings := app.settings
	settings.Plugins = normalized
	if err := config.Save(app.layout.Config, settings); err != nil {
		app.pluginStatus = "保存插件商店设置失败：" + err.Error()
		return
	}
	app.settings = settings
	app.pluginStatus = fmt.Sprintf("商店筛选已更新：%d 项", app.pluginCatalogPage.Total)
}

func (app *application) pluginStoreClick(x, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	switch {
	case y >= sy(270) && y < sy(314):
		app.savePluginCatalogURL()
		app.savePluginSearch()
		app.startPluginCatalogSync()
	case y >= sy(320) && y < sy(364):
		if len(app.pluginCatalogPage.Items) == 0 {
			app.pluginStatus = "当前筛选没有商店插件"
			break
		}
		index := 0
		for i, item := range app.pluginCatalogPage.Items {
			if item.ID == app.pluginSelected {
				index = (i + 1) % len(app.pluginCatalogPage.Items)
				break
			}
		}
		app.pluginSelected = app.pluginCatalogPage.Items[index].ID
		app.pluginStatus = "商店已选择：" + app.pluginCatalogPage.Items[index].Name
	case y >= sy(370) && y < sy(414):
		values := []string{"", "utility", "gameplay", "visuals", "other"}
		index := 0
		for i, value := range values {
			if value == app.settings.Plugins.Category {
				index = (i + 1) % len(values)
				break
			}
		}
		next := app.settings.Plugins
		next.Category, next.Page = values[index], 1
		app.applyPluginStoreConfig(next)
	case y >= sy(420) && y < sy(464):
		values := []string{"popular", "newest", "rating", "name"}
		index := 0
		for i, value := range values {
			if value == app.settings.Plugins.Sort {
				index = (i + 1) % len(values)
				break
			}
		}
		next := app.settings.Plugins
		next.Sort, next.Page = values[index], 1
		app.applyPluginStoreConfig(next)
	case y >= sy(470) && y < sy(514):
		client := win32.GetClientRect(app.hwnd)
		middle := int(win32.Scale(252, app.dpi)) + (int(client.Right)-int(win32.Scale(294, app.dpi)))/2
		next := app.settings.Plugins
		if x < middle && next.Page > 1 {
			next.Page--
		} else if x >= middle && next.Page < app.pluginCatalogPage.TotalPages {
			next.Page++
		}
		app.applyPluginStoreConfig(next)
	case y >= sy(520) && y < sy(564):
		app.startCatalogPluginInstall()
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) selectedCatalogPlugin() (plugins.CatalogItem, bool) {
	for _, item := range app.pluginCatalogPage.Items {
		if item.ID == app.pluginSelected {
			return item, true
		}
	}
	if len(app.pluginCatalogPage.Items) > 0 {
		return app.pluginCatalogPage.Items[0], true
	}
	return plugins.CatalogItem{}, false
}

func (app *application) startCatalogPluginInstall() {
	if app.pluginBusy || app.gameState.Candidate == nil {
		app.pluginStatus = "请等待当前任务结束并先完成游戏扫描"
		return
	}
	item, ok := app.selectedCatalogPlugin()
	if !ok {
		app.pluginStatus = "请先同步并选择商店插件"
		return
	}
	state := plugins.CloneState(app.pluginState)
	candidate := *app.gameState.Candidate
	layout := app.pluginLayout
	app.pluginBusy = true
	app.pluginStatus = "正在下载、校验并隔离安装 " + item.Name
	app.pluginTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		temporary, err := os.CreateTemp(layout.Staging, item.ID+"-download-*.zip")
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: "创建插件下载暂存文件失败：" + err.Error()})
			return
		}
		packagePath := temporary.Name()
		_ = temporary.Close()
		_ = os.Remove(packagePath)
		defer os.Remove(packagePath)
		if err := plugins.DownloadPackage(ctx, nil, item, packagePath); err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: "下载插件失败：" + err.Error()})
			return
		}
		result, err := plugins.InstallLocalPackage(ctx, packagePath, item, layout, candidate, &state)
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: "安装商店插件失败：" + err.Error()})
			return
		}
		app.publishPlugin(pluginUpdate{taskID: id, state: &state, status: "商店插件已安装：" + result.Manifest.Name + " " + result.Manifest.Version})
	})
}

func (app *application) publishPlugin(update pluginUpdate) {
	select {
	case app.pluginUpdates <- update:
		win32.PostMessage(app.hwnd, messagePlugins, 0, 0)
	default:
	}
}

func (app *application) startLocalPluginInstall() {
	if app.pluginBusy {
		app.pluginStatus = "已有插件任务正在执行"
		return
	}
	if app.gameState.Candidate == nil {
		app.pluginStatus = "请先在游戏管理页完成游戏路径和版本扫描"
		return
	}
	packagePath, selected, err := win32.SelectPluginPackage(app.hwnd, app.layout.Root)
	if err != nil {
		app.pluginStatus = "选择插件包失败：" + err.Error()
		return
	}
	if !selected {
		return
	}
	state := plugins.CloneState(app.pluginState)
	candidate := *app.gameState.Candidate
	layout := app.pluginLayout
	app.pluginBusy = true
	app.pluginStatus = "正在隔离区审计本地插件包；完成前不会修改活动插件"
	app.pluginTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		item, inspectErr := plugins.InspectLocalPackage(packagePath)
		if inspectErr != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: "读取插件包失败：" + inspectErr.Error()})
			return
		}
		result, installErr := plugins.InstallLocalPackage(ctx, packagePath, item, layout, candidate, &state)
		if installErr != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: "安装插件失败：" + installErr.Error()})
			return
		}
		status := "插件已安装：" + result.Manifest.Name + " " + result.Manifest.Version
		if result.RollbackReady {
			status += "；上一版本已保留用于回滚"
		}
		app.publishPlugin(pluginUpdate{taskID: id, state: &state, status: status})
	})
}

func (app *application) startPluginRollback() {
	if app.pluginBusy {
		app.pluginStatus = "已有插件任务正在执行"
		return
	}
	item, ok := app.selectedPlugin()
	if !ok || app.gameState.Candidate == nil {
		app.pluginStatus = "请先选择插件并完成游戏扫描"
		return
	}
	installed, ok := app.pluginState.Installed[item.Manifest.ID]
	if !ok || len(installed.RollbackVersions) == 0 {
		app.pluginStatus = "当前插件没有可回滚版本"
		return
	}
	version := installed.RollbackVersions[len(installed.RollbackVersions)-1]
	state := plugins.CloneState(app.pluginState)
	candidate := *app.gameState.Candidate
	layout := app.pluginLayout
	app.pluginBusy = true
	app.pluginStatus = "正在重新审计并回滚到 " + version
	app.pluginTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		result, err := plugins.Rollback(ctx, layout, &state, item.Manifest.ID, version, candidate)
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: "插件回滚失败：" + err.Error()})
			return
		}
		app.publishPlugin(pluginUpdate{taskID: id, state: &state, status: "插件已回滚到 " + result.Manifest.Version})
	})
}

func (app *application) startPluginUninstall() {
	if app.pluginBusy {
		app.pluginStatus = "已有插件任务正在执行"
		return
	}
	item, ok := app.selectedPlugin()
	if !ok {
		app.pluginStatus = "请先选择插件"
		return
	}
	if app.pluginDeleteConfirm != item.Manifest.ID {
		app.pluginDeleteConfirm = item.Manifest.ID
		app.pluginStatus = "再次单击卸载以确认删除该插件及其回滚版本"
		return
	}
	app.pluginDeleteConfirm = ""
	state := plugins.CloneState(app.pluginState)
	layout := app.pluginLayout
	app.pluginBusy = true
	app.pluginStatus = "正在事务化卸载插件"
	app.pluginTask = app.tasks.Run(func(_ context.Context, id uint64) {
		manifest, err := plugins.Uninstall(layout, &state, item.Manifest.ID)
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: "卸载插件失败：" + err.Error()})
			return
		}
		app.publishPlugin(pluginUpdate{taskID: id, state: &state, status: "插件已安全卸载：" + manifest.Name})
	})
}

func (app *application) applyNextPluginPreset() {
	item, ok := app.selectedPlugin()
	if !ok || item.SchemaPath == "" || item.ConfigPath == "" {
		app.pluginStatus = "当前插件没有声明式配置和预设"
		return
	}
	schema, err := plugins.LoadConfigSchema(item.SchemaPath)
	if err != nil || len(schema.Presets) == 0 {
		app.pluginStatus = "插件配置预设不可用"
		if err != nil {
			app.pluginStatus += "：" + err.Error()
		}
		return
	}
	values, recovered, err := plugins.ReadConfigRecovering(item.ConfigPath, schema)
	if err != nil {
		app.pluginStatus = "读取插件配置失败：" + err.Error()
		return
	}
	next := 0
	for index, preset := range schema.Presets {
		matches := true
		for field, value := range preset.Values {
			matches = matches && values[field] == value
		}
		if matches {
			next = (index + 1) % len(schema.Presets)
			break
		}
	}
	preset := schema.Presets[next]
	if err := plugins.ApplyPreset(item.ConfigPath, schema, preset.ID); err != nil {
		app.pluginStatus = "应用插件预设失败：" + err.Error()
		return
	}
	app.pluginStatus = "已应用配置预设：" + preset.Name
	if recovered != "" {
		app.pluginStatus += "；损坏配置已隔离"
	}
}

func (app *application) movePlugin(delta int) {
	item, ok := app.selectedPlugin()
	if !ok {
		app.pluginStatus = "请先选择插件"
		return
	}
	next := plugins.CloneState(app.pluginState)
	if err := plugins.Move(&next, item.Manifest.ID, delta); err != nil {
		app.pluginStatus = "无法调整顺序：" + err.Error()
		return
	}
	if err := plugins.SaveState(app.pluginLayout.State, next); err != nil {
		app.pluginStatus = "保存插件顺序失败：" + err.Error()
		return
	}
	app.pluginState = next
	app.refreshPlugins()
}

func (app *application) paintPlugins(dc win32.HDC, client win32.Rect, left int32) {
	cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
	defer win32.DeleteObject(uintptr(cardBrush))
	buttonBrush := win32.CreateSolidBrush(win32.Color(35, 40, 54))
	defer win32.DeleteObject(uintptr(buttonBrush))
	accentBrush := win32.CreateSolidBrush(win32.Color(52, 66, 112))
	defer win32.DeleteObject(uintptr(accentBrush))
	warningBrush := win32.CreateSolidBrush(win32.Color(74, 48, 35))
	defer win32.DeleteObject(uintptr(warningBrush))
	right := client.Right - win32.Scale(42, app.dpi)
	row := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		win32.FillRect(dc, &rect, brush)
		return rect
	}
	cell := func(rect win32.Rect, index, count int32) win32.Rect {
		width := (rect.Right - rect.Left) / count
		return win32.Rect{Left: rect.Left + index*width, Top: rect.Top, Right: rect.Left + (index+1)*width, Bottom: rect.Bottom}
	}
	draw := func(value string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, value, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	safe := map[bool]string{true: "开启（禁止插件注入）", false: "关闭（允许启用项注入）"}[app.settings.Plugins.SafeMode]
	draw("插件安全模式："+safe+"    单击切换", row(170, 214, warningBrush), win32.Color(255, 205, 150))
	draw("重新扫描本地插件（不联网）", row(220, 264, buttonBrush), win32.Color(225, 229, 242))
	selected := "无已安装插件"
	enabled := false
	if item, ok := app.selectedPlugin(); ok {
		selected = fmt.Sprintf("%s · %s · %s", item.DisplayName(), item.Manifest.Version, item.Manifest.Developer)
		enabled = item.Enabled
	}
	draw("当前插件："+selected+"    单击切换", row(270, 314, buttonBrush), win32.Color(225, 229, 242))
	draw("启用状态："+map[bool]string{true: "启用", false: "停用"}[enabled]+"    单击切换", row(320, 364, accentBrush), win32.Color(235, 238, 248))
	draw("在上方输入插件别名（留空恢复原名）", row(370, 414, cardBrush), win32.Color(145, 154, 180))
	actions := row(420, 464, buttonBrush)
	draw("保存别名", cell(actions, 0, 3), win32.Color(225, 229, 242))
	draw("顺序向前", cell(actions, 1, 3), win32.Color(190, 197, 216))
	draw("顺序向后", cell(actions, 2, 3), win32.Color(190, 197, 216))
	installText := "从本地 ZIP 安装或更新插件"
	if app.pluginBusy {
		installText = "插件审计/安装进行中…"
	}
	draw(installText, row(470, 514, buttonBrush), win32.Color(190, 197, 216))
	lifecycle := row(520, 564, warningBrush)
	draw("回滚上一版本", cell(lifecycle, 0, 3), win32.Color(255, 205, 150))
	draw("卸载（二次确认）", cell(lifecycle, 1, 3), win32.Color(255, 170, 150))
	draw("下一个配置预设", cell(lifecycle, 2, 3), win32.Color(255, 205, 150))
	statusText := app.pluginStatus
	if len(app.pluginWarnings) > 0 {
		statusText += " | " + app.pluginWarnings[0]
	}
	if app.pluginCatalogPage.Total > 0 {
		statusText += fmt.Sprintf(" | 商店 %d 项", app.pluginCatalogPage.Total)
	}
	draw(statusText, row(570, 614, cardBrush), win32.Color(145, 154, 180))
	draw("", row(620, 660, cardBrush), win32.Color(145, 154, 180))
}

func (app *application) paintPluginStore(dc win32.HDC, client win32.Rect, left int32) {
	cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
	defer win32.DeleteObject(uintptr(cardBrush))
	buttonBrush := win32.CreateSolidBrush(win32.Color(35, 40, 54))
	defer win32.DeleteObject(uintptr(buttonBrush))
	accentBrush := win32.CreateSolidBrush(win32.Color(52, 66, 112))
	defer win32.DeleteObject(uintptr(accentBrush))
	right := client.Right - win32.Scale(42, app.dpi)
	row := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		win32.FillRect(dc, &rect, brush)
		return rect
	}
	cell := func(rect win32.Rect, index, count int32) win32.Rect {
		width := (rect.Right - rect.Left) / count
		return win32.Rect{Left: rect.Left + index*width, Top: rect.Top, Right: rect.Left + (index+1)*width, Bottom: rect.Bottom}
	}
	draw := func(value string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, value, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	draw("", row(170, 214, cardBrush), win32.Color(225, 229, 242))
	draw("", row(220, 264, cardBrush), win32.Color(225, 229, 242))
	syncText := "保存筛选并同步 HTTPS 插件目录"
	if app.pluginBusy {
		syncText = "插件目录或安装任务进行中…"
	}
	draw(syncText, row(270, 314, accentBrush), win32.Color(235, 238, 248))
	selected := "当前页没有插件"
	installAction := "下载、复核并事务安装所选插件"
	if item, ok := app.selectedCatalogPlugin(); ok {
		selected = fmt.Sprintf("%s · %s · %s", item.Name, item.Version, item.Developer)
		if installed, exists := app.pluginState.Installed[item.ID]; exists {
			if installed.ActiveVersion == item.Version {
				selected += " · 已安装"
				installAction = "重新下载、复核并修复所选插件"
			} else {
				selected += " · 已安装 " + installed.ActiveVersion + "，目录版本 " + item.Version
				installAction = "下载、复核并更新所选插件"
			}
		}
	}
	draw("商店插件："+selected+"    单击切换", row(320, 364, buttonBrush), win32.Color(225, 229, 242))
	category := app.settings.Plugins.Category
	if category == "" {
		category = "全部"
	}
	draw("分类："+category+"    单击切换", row(370, 414, buttonBrush), win32.Color(190, 197, 216))
	draw("排序："+app.settings.Plugins.Sort+"    单击切换", row(420, 464, buttonBrush), win32.Color(190, 197, 216))
	pages := row(470, 514, buttonBrush)
	draw("上一页", cell(pages, 0, 2), win32.Color(190, 197, 216))
	draw(fmt.Sprintf("下一页    %d/%d", app.pluginCatalogPage.Page, app.pluginCatalogPage.TotalPages), cell(pages, 1, 2), win32.Color(190, 197, 216))
	draw(installAction, row(520, 564, accentBrush), win32.Color(235, 238, 248))
	draw(app.pluginStatus, row(570, 614, cardBrush), win32.Color(145, 154, 180))
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
	if event.Code == app.settings.Capture.VirtualKey {
		app.inputUIError = "该物理键已被截图快捷键使用；即使修饰键不同也可能触发输入停止逻辑"
		win32.Invalidate(app.hwnd)
		return
	}
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

func (app *application) createLaunchControls() error {
	edit, err := win32.CreateControl("EDIT", app.settings.Launch.CustomArguments, win32.WS_CHILD|win32.WS_BORDER|win32.WS_TABSTOP|win32.ES_AUTOHSCROLL, 0, 0, 100, 32, app.hwnd, 2001, app.instance)
	if err != nil {
		return err
	}
	app.customArgumentsEdit = edit
	app.editBrush = win32.CreateSolidBrush(win32.Color(25, 29, 39))
	win32.SetTextLimit(edit, 8192)
	win32.SetCueBanner(edit, "自定义启动参数（可留空，例如 -force-d3d11）")
	win32.SetControlFont(edit, app.fontBody)
	win32.EnableDarkControl(edit)
	aliasEdit, err := win32.CreateControl("EDIT", "", win32.WS_CHILD|win32.WS_BORDER|win32.WS_TABSTOP|win32.ES_AUTOHSCROLL, 0, 0, 100, 32, app.hwnd, 2002, app.instance)
	if err != nil {
		return err
	}
	app.pluginAliasEdit = aliasEdit
	win32.SetTextLimit(aliasEdit, 64)
	win32.SetCueBanner(aliasEdit, "插件别名（留空恢复原名）")
	win32.SetControlFont(aliasEdit, app.fontBody)
	win32.EnableDarkControl(aliasEdit)
	catalogEdit, err := win32.CreateControl("EDIT", app.settings.Plugins.CatalogURL, win32.WS_CHILD|win32.WS_BORDER|win32.WS_TABSTOP|win32.ES_AUTOHSCROLL, 0, 0, 100, 32, app.hwnd, 2003, app.instance)
	if err != nil {
		return err
	}
	app.pluginCatalogEdit = catalogEdit
	win32.SetTextLimit(catalogEdit, 2048)
	win32.SetCueBanner(catalogEdit, "HTTPS 插件目录 URL（留空则不联网）")
	win32.SetControlFont(catalogEdit, app.fontBody)
	win32.EnableDarkControl(catalogEdit)
	searchEdit, err := win32.CreateControl("EDIT", app.settings.Plugins.Search, win32.WS_CHILD|win32.WS_BORDER|win32.WS_TABSTOP|win32.ES_AUTOHSCROLL, 0, 0, 100, 32, app.hwnd, 2004, app.instance)
	if err != nil {
		return err
	}
	app.pluginSearchEdit = searchEdit
	win32.SetTextLimit(searchEdit, 128)
	win32.SetCueBanner(searchEdit, "搜索插件名称、作者、描述或标签")
	win32.SetControlFont(searchEdit, app.fontBody)
	win32.EnableDarkControl(searchEdit)
	app.layoutLaunchControls()
	app.updateLaunchControlVisibility()
	return nil
}

func (app *application) layoutLaunchControls() {
	if app.customArgumentsEdit == 0 || app.hwnd == 0 {
		return
	}
	client := win32.GetClientRect(app.hwnd)
	left := win32.Scale(252, app.dpi)
	top := win32.Scale(352, app.dpi)
	right := client.Right - win32.Scale(42, app.dpi)
	height := win32.Scale(36, app.dpi)
	win32.SetWindowPos(app.customArgumentsEdit, win32.Rect{Left: left, Top: top, Right: right, Bottom: top + height}, win32.SWP_NOZORDER)
	if app.pluginAliasEdit != 0 {
		aliasTop := win32.Scale(370, app.dpi)
		win32.SetWindowPos(app.pluginAliasEdit, win32.Rect{Left: left, Top: aliasTop, Right: right, Bottom: aliasTop + height}, win32.SWP_NOZORDER)
	}
	if app.pluginCatalogEdit != 0 {
		catalogTop := win32.Scale(170, app.dpi)
		win32.SetWindowPos(app.pluginCatalogEdit, win32.Rect{Left: left, Top: catalogTop, Right: right, Bottom: catalogTop + height}, win32.SWP_NOZORDER)
	}
	if app.pluginSearchEdit != 0 {
		searchTop := win32.Scale(220, app.dpi)
		win32.SetWindowPos(app.pluginSearchEdit, win32.Rect{Left: left, Top: searchTop, Right: right, Bottom: searchTop + height}, win32.SWP_NOZORDER)
	}
}

func (app *application) updateLaunchControlVisibility() {
	if app.customArgumentsEdit != 0 && app.selected == 1 {
		win32.ShowWindow(app.customArgumentsEdit, win32.SW_SHOWNORMAL)
	} else if app.customArgumentsEdit != 0 {
		win32.ShowWindow(app.customArgumentsEdit, win32.SW_HIDE)
	}
	if app.pluginAliasEdit != 0 && app.selected == 8 {
		app.syncPluginAliasEdit()
		win32.ShowWindow(app.pluginAliasEdit, win32.SW_SHOWNORMAL)
	} else if app.pluginAliasEdit != 0 {
		win32.ShowWindow(app.pluginAliasEdit, win32.SW_HIDE)
	}
	if app.pluginCatalogEdit != 0 && app.selected == 9 {
		win32.ShowWindow(app.pluginCatalogEdit, win32.SW_SHOWNORMAL)
	} else if app.pluginCatalogEdit != 0 {
		win32.ShowWindow(app.pluginCatalogEdit, win32.SW_HIDE)
	}
	if app.pluginSearchEdit != 0 && app.selected == 9 {
		win32.ShowWindow(app.pluginSearchEdit, win32.SW_SHOWNORMAL)
	} else if app.pluginSearchEdit != 0 {
		win32.ShowWindow(app.pluginSearchEdit, win32.SW_HIDE)
	}
}

func (app *application) syncPluginAliasEdit() {
	if app.pluginAliasEdit == 0 {
		return
	}
	alias := ""
	if item, ok := app.selectedPlugin(); ok {
		alias = item.Alias
	}
	win32.SetWindowText(app.pluginAliasEdit, alias)
}

func (app *application) syncLaunchConfig() bool {
	if app.customArgumentsEdit != 0 {
		app.settings.Launch.CustomArguments = win32.GetWindowText(app.customArgumentsEdit)
	}
	normalized, err := app.settings.Launch.Normalized()
	if err != nil {
		app.launchUIError = err.Error()
		return false
	}
	app.settings.Launch = normalized
	if err := config.Save(app.layout.Config, app.settings); err != nil {
		app.launchUIError = "保存启动设置：" + err.Error()
		return false
	}
	app.launchUIError = ""
	return true
}

func (app *application) startLauncher() error {
	engine, err := launch.NewEngine(launch.NativeStarter{}, game.RunningProcesses, func(snapshot launch.Snapshot) {
		select {
		case app.launchUpdates <- snapshot:
		default:
			select {
			case <-app.launchUpdates:
			default:
			}
			select {
			case app.launchUpdates <- snapshot:
			default:
			}
		}
		win32.PostMessage(app.hwnd, messageLaunch, 0, 0)
	})
	if err != nil {
		return err
	}
	app.launchEngine = engine
	app.launchSnap = engine.Snapshot()
	return nil
}

func (app *application) applyPostLaunch(behavior launch.PostBehavior) {
	switch behavior {
	case launch.PostMinimize:
		win32.ShowWindow(app.hwnd, win32.SW_HIDE)
	case launch.PostExit:
		app.requestShutdown()
	}
}

func (app *application) scheduleBetterGI() {
	if !app.settings.LocalEnhance.BetterGIEnabled {
		return
	}
	delay := time.Duration(app.settings.LocalEnhance.BetterGIDelayMS) * time.Millisecond
	app.betterGITask = app.tasks.Run(func(ctx context.Context, _ uint64) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if err := localenhance.StartBetterGI(); err != nil {
			app.logger.Error("BetterGI protocol launch failed; game remains running", map[string]any{"error": err.Error()})
		} else {
			app.logger.Info("BetterGI protocol launch requested", nil)
		}
	})
}

func (app *application) publishServer(id uint64, state serverViewState, refresh bool) {
	select {
	case app.serverUpdates <- serverUpdate{taskID: id, state: state, refresh: refresh}:
	default:
		select {
		case <-app.serverUpdates:
		default:
		}
		app.serverUpdates <- serverUpdate{taskID: id, state: state, refresh: refresh}
	}
	win32.PostMessage(app.hwnd, messageServer, 0, 0)
}

func (app *application) serverClick(_, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	state := app.serverState
	if state.Busy {
		if y >= sy(270) && y < sy(314) {
			app.tasks.Cancel(app.serverTask)
			state.Status = "正在取消区服任务…"
			app.serverState = state
			win32.Invalidate(app.hwnd)
		}
		return
	}
	switch {
	case y >= sy(170) && y < sy(214):
		state.Advanced = !state.Advanced
		if app.gameState.Candidate != nil && app.gameState.Candidate.Server == game.ServerGlobal {
			state.AdvancedTarget = localenhance.AdvancedMainland
		} else {
			state.AdvancedTarget = localenhance.AdvancedGlobal
		}
		if state.Transaction != nil {
			_ = state.Transaction.Abort()
		}
		state.Transaction = nil
		state.Plan = resources.RepairPlan{}
		state.Confirm = false
		state.Status = "切换方式已更改，请重新生成变更预览"
		state.Error = ""
		app.serverState = state
	case y >= sy(220) && y < sy(264):
		if state.Advanced {
			if state.AdvancedTarget == localenhance.AdvancedGlobal {
				state.AdvancedTarget = localenhance.AdvancedMainland
			} else {
				state.AdvancedTarget = localenhance.AdvancedGlobal
			}
		} else if state.Target == localenhance.QuickOfficial {
			state.Target = localenhance.QuickBilibili
		} else {
			state.Target = localenhance.QuickOfficial
		}
		if state.Transaction != nil {
			_ = state.Transaction.Abort()
		}
		state.Transaction = nil
		state.Plan = resources.RepairPlan{}
		state.Confirm = false
		state.Status = "目标已切换，请重新生成变更预览"
		state.Error = ""
		app.serverState = state
	case y >= sy(270) && y < sy(314):
		app.startServerPlan()
		return
	case y >= sy(320) && y < sy(364):
		if state.Transaction == nil {
			state.Error = "请先生成区服变更预览"
		} else if len(app.gameState.Running) > 0 {
			state.Error = "游戏运行时不能切换区服"
		} else if !state.Confirm {
			state.Confirm = true
			state.Status = "再次单击确认提交；所有替换和删除均有事务备份"
			state.Error = ""
		} else {
			app.serverState = state
			app.startServerCommit()
			return
		}
		app.serverState = state
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) startServerPlan() {
	if app.gameState.Candidate == nil {
		app.serverState.Error = "请先在游戏管理页选择有效游戏目录"
		win32.Invalidate(app.hwnd)
		return
	}
	if !app.serverState.Advanced && app.gameState.Candidate.Server == game.ServerGlobal {
		app.serverState.Error = "官服/B服快速切换仅适用于已识别的国服 YuanShen.exe"
		win32.Invalidate(app.hwnd)
		return
	}
	root := app.gameState.Candidate.Root
	state := app.serverState
	if state.Transaction != nil {
		_ = state.Transaction.Abort()
	}
	state.Busy, state.Confirm, state.Error = true, false, ""
	state.Status = "正在读取官方渠道 SDK 清单并准备隔离文件…"
	if state.Advanced {
		state.Status = "正在读取目标区服 Sophon 清单并准备完整差异；可能需要较长下载…"
	}
	state.Transaction = nil
	app.serverState = state
	app.serverTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		transaction, err := resources.NewTransaction(app.layout.Staging, root, fmt.Sprintf("server-%d", time.Now().UTC().UnixNano()))
		if err == nil {
			err = transaction.Prepare()
		}
		if err == nil && state.Advanced {
			var conversion localenhance.AdvancedConversion
			conversion, err = localenhance.PrepareAdvancedServerConversion(ctx, nil, root, transaction.StagingRoot, state.AdvancedTarget)
			state.Plan = conversion.Plan
		} else if err == nil {
			state.Plan, err = localenhance.PrepareQuickServerSwitch(ctx, nil, root, transaction.StagingRoot, state.Target)
		}
		state.Busy = false
		if err != nil {
			if transaction != nil {
				_ = transaction.Abort()
			}
			if errors.Is(err, context.Canceled) {
				state.Status, state.Error = "区服预览已取消；游戏文件未修改", ""
			} else {
				state.Status, state.Error = "区服预览失败；游戏文件未修改", err.Error()
			}
		} else {
			state.Transaction = transaction
			state.Status = fmt.Sprintf("预览完成：%d 项计划，确认前不会修改游戏目录", len(state.Plan.Items))
		}
		app.publishServer(id, state, false)
	})
}

func (app *application) startServerCommit() {
	state := app.serverState
	state.Busy, state.Confirm, state.Error = true, false, ""
	state.Status = "正在提交区服事务；失败将自动逆序回滚…"
	app.serverState = state
	app.serverTask = app.tasks.Run(func(_ context.Context, id uint64) {
		err := state.Transaction.Commit(state.Plan)
		state.Busy = false
		if err != nil {
			state.Status, state.Error = "区服切换失败；原配置和 SDK 已恢复", err.Error()
		} else {
			state.Status = "区服切换完成"
			state.Transaction = nil
			state.Plan = resources.RepairPlan{}
		}
		app.publishServer(id, state, err == nil)
	})
}

func (app *application) paintServer(dc win32.HDC, client win32.Rect, left int32) {
	cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
	defer win32.DeleteObject(uintptr(cardBrush))
	buttonBrush := win32.CreateSolidBrush(win32.Color(35, 40, 54))
	defer win32.DeleteObject(uintptr(buttonBrush))
	accentBrush := win32.CreateSolidBrush(win32.Color(52, 66, 112))
	defer win32.DeleteObject(uintptr(accentBrush))
	right := client.Right - win32.Scale(42, app.dpi)
	row := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		win32.FillRect(dc, &rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	state := app.serverState
	mode := "官服/B服快速切换"
	if state.Advanced {
		mode = "国服/国际服高级转换"
	}
	draw("方式："+mode+"    单击切换", row(170, 214, accentBrush), win32.Color(235, 238, 248))
	target := state.Target.String()
	if state.Advanced {
		target = state.AdvancedTarget.String()
	}
	draw("目标："+target+"    单击切换", row(220, 264, accentBrush), win32.Color(235, 238, 248))
	planText := "生成变更预览"
	if state.Busy {
		planText = "取消当前区服任务"
	}
	draw(planText, row(270, 314, buttonBrush), win32.Color(225, 229, 242))
	commit := "提交切换（需要先生成预览）"
	if state.Transaction != nil {
		commit = fmt.Sprintf("提交 %d 项变更", len(state.Plan.Items))
	}
	if state.Confirm {
		commit = "再次单击确认事务提交"
	}
	draw(commit, row(320, 364, buttonBrush), win32.Color(225, 229, 242))
	status, color := state.Status, win32.Color(145, 154, 180)
	if state.Error != "" {
		status, color = state.Error, win32.Color(255, 126, 126)
	}
	draw(status, row(376, 420, cardBrush), color)
	if state.Transaction != nil {
		installs, deletes, moves := 0, 0, 0
		for _, item := range state.Plan.Items {
			if item.Action == resources.ActionDelete {
				deletes++
			} else if item.Action == resources.ActionMove {
				moves++
			} else if item.Action != resources.ActionKeep {
				installs++
			} else {
				continue
			}
		}
		draw(fmt.Sprintf("安装/替换 %d · 改名 %d · 可回滚删除 %d · 下载 %s", installs, moves, deletes, formatBytes(uint64(state.Plan.DownloadBytes))), row(426, 470, cardBrush), win32.Color(190, 197, 216))
	}
	draw("高级转换会先校验并下载目标差异，再统一提交目录/EXE 改名、替换和配置。", row(482, 526, cardBrush), win32.Color(166, 174, 197))
	draw("不会清空整个 Plugins；任一步失败均按反向日志恢复原区服布局。", row(532, 576, cardBrush), win32.Color(126, 136, 160))
}

func (app *application) saveLocalEnhance() bool {
	if err := config.Save(app.layout.Config, app.settings); err != nil {
		app.localStatus = "保存设置失败：" + err.Error()
		return false
	}
	return true
}

func (app *application) localEnhanceClick(_, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	settings := &app.settings.LocalEnhance
	switch {
	case y >= sy(170) && y < sy(214):
		settings.HDR.Enabled = !settings.HDR.Enabled
		app.localStatus = "HDR 目标状态已更改；单击“应用 HDR”后写入"
		app.saveLocalEnhance()
	case y >= sy(220) && y < sy(258):
		presets := []localenhance.HDRConfig{
			{Enabled: settings.HDR.Enabled, MaxLuminance: 1000, SceneLuminance: 300, UILuminance: 350},
			{Enabled: settings.HDR.Enabled, MaxLuminance: 1200, SceneLuminance: 320, UILuminance: 380},
			{Enabled: settings.HDR.Enabled, MaxLuminance: 1600, SceneLuminance: 400, UILuminance: 450},
		}
		index := 0
		for i, preset := range presets {
			if preset.MaxLuminance == settings.HDR.MaxLuminance && preset.SceneLuminance == settings.HDR.SceneLuminance && preset.UILuminance == settings.HDR.UILuminance {
				index = (i + 1) % len(presets)
				break
			}
		}
		settings.HDR = presets[index]
		app.localStatus = "HDR 亮度预设已更改，尚未写入注册表"
		app.saveLocalEnhance()
	case y >= sy(264) && y < sy(302):
		if app.gameState.Candidate == nil || app.gameState.Candidate.Server == game.ServerGlobal {
			app.localStatus = "HDR 注册表适配仅用于已识别的国服目录"
			break
		}
		backup := app.layout.Data + string(os.PathSeparator) + "hdr-registry-backup.json"
		if err := localenhance.ApplyHDRWithBackup(localenhance.NativeRegistry{}, settings.HDR, backup); err != nil {
			app.localStatus = "应用 HDR 失败，旧值已保留或恢复：" + err.Error()
		} else {
			app.localStatus = "HDR 已应用；原始注册表值已备份"
		}
	case y >= sy(308) && y < sy(346):
		backup := app.layout.Data + string(os.PathSeparator) + "hdr-registry-backup.json"
		if err := localenhance.RestoreHDRBackup(localenhance.NativeRegistry{}, backup); err != nil {
			app.localStatus = "恢复 HDR 备份失败：" + err.Error()
		} else {
			if current, _, err := localenhance.ReadHDR(localenhance.NativeRegistry{}); err == nil {
				settings.HDR = current
				app.saveLocalEnhance()
			}
			app.localStatus = "HDR 原始注册表值已恢复"
		}
	case y >= sy(352) && y < sy(390):
		initial := ""
		if settings.StartupSoundPath != "" {
			initial = filepath.Dir(settings.StartupSoundPath)
		}
		path, selected, err := win32.SelectWaveFile(app.hwnd, initial)
		if err != nil {
			app.localStatus = err.Error()
		} else if selected {
			if err := localenhance.PlayStartupSound(path); err != nil {
				app.localStatus = "WAV 试听失败：" + err.Error()
			} else {
				settings.StartupSoundPath = path
				settings.StartupSoundEnabled = true
				app.saveLocalEnhance()
				app.localStatus = "启动声音已选择并试听"
			}
		}
	case y >= sy(396) && y < sy(434):
		settings.StartupSoundEnabled = !settings.StartupSoundEnabled
		app.saveLocalEnhance()
		app.localStatus = "启动声音已" + map[bool]string{true: "启用", false: "停用"}[settings.StartupSoundEnabled]
	case y >= sy(440) && y < sy(478):
		settings.BetterGIEnabled = !settings.BetterGIEnabled
		app.saveLocalEnhance()
		app.localStatus = "BetterGI 联动已" + map[bool]string{true: "启用", false: "停用"}[settings.BetterGIEnabled]
	case y >= sy(484) && y < sy(522):
		delays := []int{0, 2000, 5000, 10000, 30000, 60000}
		index := 0
		for i, delay := range delays {
			if delay == settings.BetterGIDelayMS {
				index = (i + 1) % len(delays)
				break
			}
		}
		settings.BetterGIDelayMS = delays[index]
		app.saveLocalEnhance()
		app.localStatus = "BetterGI 延迟已更新"
	case y >= sy(528) && y < sy(566):
		info, err := localenhance.AuditBetterGI()
		if err != nil {
			app.localStatus = "BetterGI 审计失败：" + err.Error()
		} else if !info.Registered {
			app.localStatus = "未发现 BetterGI URL Scheme；不会影响纯净启动"
		} else if err := localenhance.StartBetterGI(); err != nil {
			app.localStatus = "BetterGI 测试启动失败：" + err.Error()
		} else {
			app.localStatus = "已向核验后的 BetterGI URL Scheme 发送启动请求"
		}
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) paintLocalEnhance(dc win32.HDC, client win32.Rect, left int32) {
	cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
	defer win32.DeleteObject(uintptr(cardBrush))
	buttonBrush := win32.CreateSolidBrush(win32.Color(35, 40, 54))
	defer win32.DeleteObject(uintptr(buttonBrush))
	accentBrush := win32.CreateSolidBrush(win32.Color(52, 66, 112))
	defer win32.DeleteObject(uintptr(accentBrush))
	right := client.Right - win32.Scale(42, app.dpi)
	row := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		win32.FillRect(dc, &rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	settings := app.settings.LocalEnhance
	draw("HDR 强制状态："+map[bool]string{true: "开启", false: "关闭"}[settings.HDR.Enabled]+"    单击切换目标", row(170, 214, accentBrush), win32.Color(235, 238, 248))
	draw(fmt.Sprintf("亮度：峰值 %d · 场景 %d · UI %d    单击切换安全预设", settings.HDR.MaxLuminance, settings.HDR.SceneLuminance, settings.HDR.UILuminance), row(220, 258, buttonBrush), win32.Color(225, 229, 242))
	draw("应用 HDR（先备份，再写入）", row(264, 302, buttonBrush), win32.Color(225, 229, 242))
	draw("恢复上一次 HDR 原始注册表值", row(308, 346, buttonBrush), win32.Color(225, 229, 242))
	sound := valueOrUnknown(settings.StartupSoundPath)
	draw("选择并试听 WAV："+sound, row(352, 390, buttonBrush), win32.Color(190, 197, 216))
	draw("启动声音："+map[bool]string{true: "启用", false: "停用"}[settings.StartupSoundEnabled], row(396, 434, buttonBrush), win32.Color(190, 197, 216))
	draw("BetterGI 协议联动："+map[bool]string{true: "启用", false: "停用"}[settings.BetterGIEnabled], row(440, 478, buttonBrush), win32.Color(190, 197, 216))
	draw(fmt.Sprintf("BetterGI 启动延迟：%.1f 秒    单击切换", float64(settings.BetterGIDelayMS)/1000), row(484, 522, buttonBrush), win32.Color(190, 197, 216))
	draw("审计并测试 BetterGI URL Scheme（不会按名称结束进程）", row(528, 566, buttonBrush), win32.Color(190, 197, 216))
	status := app.localStatus
	if status == "" {
		status = "本页操作失败不会阻止纯净游戏启动"
	}
	draw(status, row(572, 610, cardBrush), win32.Color(145, 154, 180))
}

func (app *application) gameClick(x, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	switch {
	case y >= sy(170) && y < sy(214):
		if app.gameState.Candidate == nil || app.launchEngine == nil || !app.syncLaunchConfig() {
			app.launchUIError = "请先选择并完成游戏扫描"
			break
		}
		app.launchUIError = ""
		if err := app.launchEngine.Launch(*app.gameState.Candidate, app.settings.Launch); err != nil {
			app.launchUIError = err.Error()
		}
	case y >= sy(220) && y < sy(258):
		app.settings.Launch.WindowMode = (app.settings.Launch.WindowMode + 1) % 4
		app.syncLaunchConfig()
	case y >= sy(264) && y < sy(302):
		presets := [][2]int{{1280, 720}, {1920, 1080}, {2560, 1440}, {3840, 2160}, {0, 0}}
		index := 0
		for i, preset := range presets {
			if preset[0] == app.settings.Launch.Width && preset[1] == app.settings.Launch.Height {
				index = (i + 1) % len(presets)
				break
			}
		}
		app.settings.Launch.Width, app.settings.Launch.Height = presets[index][0], presets[index][1]
		app.syncLaunchConfig()
	case y >= sy(308) && y < sy(346):
		midpoint := int(win32.Scale(650, app.dpi))
		if x < midpoint {
			app.settings.Launch.Monitor = (app.settings.Launch.Monitor + 1) % (win32.MonitorCount() + 1)
		} else {
			app.settings.Launch.PostBehavior = (app.settings.Launch.PostBehavior + 1) % 3
		}
		app.syncLaunchConfig()
	case y >= sy(396) && y < sy(434):
		contentLeft := int(win32.Scale(252, app.dpi))
		clientRight := int(win32.GetClientRect(app.hwnd).Right - win32.Scale(42, app.dpi))
		column := (x - contentLeft) * 3 / max(1, clientRight-contentLeft)
		switch column {
		case 0:
			if app.gameState.Scanning {
				app.tasks.Cancel(app.gameTask)
				app.gameState.Scanning = false
				app.gameState.Status = "扫描已取消"
			} else {
				app.startGameScan("")
			}
		case 1:
			selected, ok, err := win32.SelectExecutable(app.hwnd, app.settings.Game.Path)
			if err != nil {
				app.gameState.Error = err.Error()
				win32.Invalidate(app.hwnd)
				return
			}
			if !ok {
				return
			}
			candidate, err := game.InspectRoot(selected, "")
			if err != nil {
				app.gameState.Error = err.Error()
				win32.Invalidate(app.hwnd)
				return
			}
			app.settings.Game.Path = candidate.Root
			app.settings.Game.CustomExecutable = ""
			if !strings.EqualFold(candidate.ExeName, "YuanShen.exe") && !strings.EqualFold(candidate.ExeName, "GenshinImpact.exe") {
				app.settings.Game.CustomExecutable = candidate.ExeName
			}
			if err := config.Save(app.layout.Config, app.settings); err != nil {
				app.gameState.Error = "保存游戏路径：" + err.Error()
				win32.Invalidate(app.hwnd)
				return
			}
			app.startGameScan(candidate.Root)
		case 2:
			if app.gameState.Candidate == nil || !app.syncLaunchConfig() {
				app.shortcutStatus = "请先选择游戏"
				break
			}
			path, err := launch.CreateDesktopShortcut("原神 - Genshin Tools", *app.gameState.Candidate, app.settings.Launch)
			if err != nil {
				app.shortcutStatus = "快捷方式失败：" + err.Error()
			} else {
				app.shortcutStatus = "已创建：" + path
			}
		}
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) startGameScan(manualRoot string) {
	if app.gameTask != 0 {
		app.tasks.Cancel(app.gameTask)
	}
	app.gameState = gameViewState{Scanning: true, Status: "正在只读扫描本机游戏…"}
	win32.Invalidate(app.hwnd)
	gameSettings := app.settings.Game
	taskID := app.tasks.Run(func(ctx context.Context, id uint64) {
		state := gameViewState{Scanning: true, Status: "正在验证候选路径…"}
		publish := func() {
			update := gameUpdate{taskID: id, state: state}
			select {
			case app.gameUpdates <- update:
			default:
				select {
				case <-app.gameUpdates:
				default:
				}
				select {
				case app.gameUpdates <- update:
				default:
				}
			}
			win32.PostMessage(app.hwnd, messageGame, 0, 0)
		}

		var candidate game.Candidate
		var err error
		if manualRoot != "" {
			candidate, err = game.InspectRoot(manualRoot, gameSettings.CustomExecutable)
			state.CandidateCount = 1
		} else if gameSettings.Path != "" {
			candidate, err = game.InspectRoot(gameSettings.Path, gameSettings.CustomExecutable)
			state.CandidateCount = 1
		}
		if manualRoot == "" && (gameSettings.Path == "" || err != nil) {
			var discovery game.Discovery
			discovery, err = game.AutoDiscover(ctx, "", gameSettings.CustomExecutable)
			state.CandidateCount = len(discovery.Candidates)
			if err == nil {
				candidate, err = game.SelectSingle(discovery)
			}
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			state.Scanning = false
			state.Error = err.Error()
			if state.CandidateCount > 1 {
				state.Status = fmt.Sprintf("发现 %d 个安装，请手动选择游戏 EXE", state.CandidateCount)
			} else {
				state.Status = "未找到有效游戏安装"
			}
			publish()
			return
		}
		state.Candidate = &candidate
		state.Status = "正在计算目录大小（可取消）…"
		publish()
		total, skipped, sizeErr := game.DirectorySize(ctx, candidate.Root, func(progress game.SizeProgress) {
			state.Size = progress
			publish()
		})
		if errors.Is(sizeErr, context.Canceled) {
			return
		}
		state.Size, state.Skipped = total, skipped
		state.Scanning = false
		if sizeErr != nil {
			state.Error = sizeErr.Error()
		}
		state.Status = "只读扫描完成"
		state.Running, _ = game.RunningProcesses(candidate)
		publish()

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				state.Running, _ = game.RunningProcesses(candidate)
				publish()
			}
		}
	})
	app.gameTask = taskID
}

func (app *application) paintGame(dc win32.HDC, client win32.Rect, left int32) {
	cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
	defer win32.DeleteObject(uintptr(cardBrush))
	buttonBrush := win32.CreateSolidBrush(win32.Color(35, 40, 54))
	defer win32.DeleteObject(uintptr(buttonBrush))
	accentBrush := win32.CreateSolidBrush(win32.Color(52, 66, 112))
	defer win32.DeleteObject(uintptr(accentBrush))
	right := client.Right - win32.Scale(42, app.dpi)
	row := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		win32.FillRect(dc, &rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	launchText := "纯净启动游戏"
	if app.launchSnap.State == launch.StateStarting {
		launchText = "正在启动…"
	} else if app.launchSnap.State == launch.StateRunning {
		launchText = fmt.Sprintf("游戏运行中（本次 PID %d）", app.launchSnap.PID)
	}
	draw(launchText, row(170, 214, accentBrush), win32.Color(235, 238, 248))
	draw("窗口模式："+app.settings.Launch.WindowMode.String()+"    单击切换", row(220, 258, buttonBrush), win32.Color(225, 229, 242))
	resolution := "游戏默认"
	if app.settings.Launch.Width > 0 {
		resolution = fmt.Sprintf("%d × %d", app.settings.Launch.Width, app.settings.Launch.Height)
	}
	draw("分辨率："+resolution+"    单击切换预设", row(264, 302, buttonBrush), win32.Color(225, 229, 242))
	midpoint := left + (right-left)/2
	monitorRect := win32.Rect{Left: left, Top: win32.Scale(308, app.dpi), Right: midpoint - win32.Scale(4, app.dpi), Bottom: win32.Scale(346, app.dpi)}
	postRect := win32.Rect{Left: midpoint + win32.Scale(4, app.dpi), Top: monitorRect.Top, Right: right, Bottom: monitorRect.Bottom}
	win32.FillRect(dc, &monitorRect, buttonBrush)
	win32.FillRect(dc, &postRect, buttonBrush)
	monitor := "默认"
	if app.settings.Launch.Monitor > 0 {
		monitor = fmt.Sprintf("显示器 %d", app.settings.Launch.Monitor)
	}
	postNames := []string{"保持启动器", "最小化到托盘", "启动后退出"}
	draw("目标："+monitor, monitorRect, win32.Color(225, 229, 242))
	draw("之后："+postNames[app.settings.Launch.PostBehavior], postRect, win32.Color(225, 229, 242))

	actionWidth := (right - left) / 3
	actions := []string{"重新扫描/取消", "选择游戏 EXE", "创建桌面快捷方式"}
	for index, text := range actions {
		rect := win32.Rect{Left: left + int32(index)*actionWidth, Top: win32.Scale(396, app.dpi), Right: left + int32(index+1)*actionWidth - win32.Scale(6, app.dpi), Bottom: win32.Scale(434, app.dpi)}
		win32.FillRect(dc, &rect, buttonBrush)
		draw(text, rect, win32.Color(190, 197, 216))
	}

	state := app.gameState
	status := state.Status
	statusColor := win32.Color(145, 154, 180)
	if app.launchUIError != "" {
		status, statusColor = "启动错误："+app.launchUIError, win32.Color(255, 126, 126)
	} else if app.shortcutStatus != "" {
		status = app.shortcutStatus
	} else if app.launchSnap.State == launch.StateExited {
		status = fmt.Sprintf("游戏已退出，代码 %d；启动器可再次启动", app.launchSnap.ExitCode)
	} else if app.launchSnap.State == launch.StateFailed {
		status, statusColor = "游戏进程错误："+app.launchSnap.LastError, win32.Color(255, 126, 126)
	} else if state.Error != "" {
		status, statusColor = state.Error, win32.Color(255, 126, 126)
	}
	draw(status, row(440, 476, cardBrush), statusColor)
	if state.Candidate == nil {
		return
	}
	candidate := state.Candidate
	draw("路径："+candidate.Root, row(482, 518, cardBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf("%s    版本 %s    %s", candidate.ExeName, valueOrUnknown(candidate.Version), candidate.Server.String()), row(524, 560, cardBrush), win32.Color(190, 197, 216))
	running := "未运行"
	if len(state.Running) > 0 {
		if state.Running[0].VerifiedPath {
			running = fmt.Sprintf("运行中 PID %d（路径已核验）", state.Running[0].PID)
		} else {
			running = fmt.Sprintf("可能运行 PID %d（仅名称匹配）", state.Running[0].PID)
		}
	}
	draw(fmt.Sprintf("%s · %d 文件 · 跳过 %d · %s", formatBytes(state.Size.Bytes), state.Size.Files, state.Skipped, running), row(566, 602, cardBrush), win32.Color(166, 174, 197))
}

func (app *application) resourceClick(_, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	state := app.resourceState
	if state.Busy {
		if y >= sy(170) && y < sy(214) {
			app.tasks.Cancel(app.resourceTask)
			app.resourceState.Status = "正在取消资源任务…"
			win32.Invalidate(app.hwnd)
		}
		return
	}
	switch {
	case y >= sy(170) && y < sy(214):
		app.startResourcePlan()
	case y >= sy(220) && y < sy(258):
		languages := []string{"zh-cn", "en-us", "ja-jp", "ko-kr"}
		index := 0
		for i, language := range languages {
			if language == state.Language {
				index = (i + 1) % len(languages)
				break
			}
		}
		state.Language = languages[index]
		state.HasPlan = false
		state.Confirm = false
		state.Status = "语音包已切换，请重新检查资源"
		state.Error = ""
		app.resourceState = state
	case y >= sy(264) && y < sy(302):
		state.PreDownload = !state.PreDownload
		state.HasPlan = false
		state.Confirm = false
		state.Status = "资源分支已切换，请重新检查"
		state.Error = ""
		app.resourceState = state
	case y >= sy(308) && y < sy(352):
		if !state.HasPlan {
			state.Error = "请先生成修复计划"
		} else if len(state.Plan.Changes()) == 0 && state.PreDownload {
			state.Status = "当前文件均已通过校验，无需修复"
			state.Error = ""
		} else if len(app.gameState.Running) > 0 {
			state.Error = "游戏运行时不能替换资源，请先退出游戏"
		} else if !state.Confirm {
			state.Confirm = true
			state.Status = "再次单击以确认下载并事务替换列出的文件"
			state.Error = ""
		} else {
			app.resourceState = state
			app.startResourceApply()
			return
		}
		app.resourceState = state
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) startResourcePlan() {
	if app.gameState.Candidate == nil {
		app.resourceState.Error = "请先在游戏管理页选择游戏目录"
		win32.Invalidate(app.hwnd)
		return
	}
	root := app.gameState.Candidate.Root
	state := app.resourceState
	state.Busy, state.Confirm, state.HasPlan = true, false, false
	state.Error = ""
	state.Status = "正在读取官方资源目录…"
	app.resourceState = state
	publish := func(id uint64, next resourceViewState, refresh bool) {
		select {
		case app.resourceUpdates <- resourceUpdate{taskID: id, state: next, refresh: refresh}:
		default:
			select {
			case <-app.resourceUpdates:
			default:
			}
			app.resourceUpdates <- resourceUpdate{taskID: id, state: next, refresh: refresh}
		}
		win32.PostMessage(app.hwnd, messageResource, 0, 0)
	}
	app.resourceTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		provider := resources.NewSophonProvider()
		branches, err := provider.FetchBranches(ctx)
		if err == nil {
			branch := branches.Current
			if state.PreDownload {
				if branches.PreDownload == nil {
					err = errors.New("官方当前没有开放预下载分支")
				} else {
					branch = *branches.PreDownload
				}
			}
			if err == nil {
				provider.BuildURL, err = branch.BuildURL()
				state.Version = branch.Tag
			}
		}
		var catalog resources.SophonCatalog
		if err == nil {
			catalog, err = provider.FetchCatalog(ctx)
		}
		if err == nil {
			state.Status = "正在校验在线 manifest…"
			publish(id, state, false)
			state.Manifest, err = provider.LoadManifest(ctx, catalog, "game", state.Language)
			state.Version = catalog.Version
		}
		if err == nil {
			state.Status = "正在逐文件生成只读修复计划…"
			publish(id, state, false)
			state.Plan, err = resources.BuildRepairPlanContext(ctx, root, state.Manifest)
		}
		state.Busy = false
		if err != nil {
			if errors.Is(err, context.Canceled) {
				state.Status, state.Error = "资源检查已取消", ""
			} else {
				state.Error = err.Error()
				state.Status = "资源检查失败，未修改游戏文件"
			}
		} else {
			state.HasPlan = true
			state.Status = fmt.Sprintf("计划完成：%d 个文件需下载或修复", len(state.Plan.Changes()))
		}
		publish(id, state, false)
	})
}

func (app *application) startResourceApply() {
	state := app.resourceState
	root := app.gameState.Candidate.Root
	state.Busy = true
	state.Confirm = false
	state.Error = ""
	state.Status = "正在准备隔离事务…"
	app.resourceState = state
	publish := func(id uint64, next resourceViewState, refresh bool) {
		select {
		case app.resourceUpdates <- resourceUpdate{taskID: id, state: next, refresh: refresh}:
		default:
			select {
			case <-app.resourceUpdates:
			default:
			}
			app.resourceUpdates <- resourceUpdate{taskID: id, state: next, refresh: refresh}
		}
		win32.PostMessage(app.hwnd, messageResource, 0, 0)
	}
	app.resourceTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		transactionID := "preload-" + state.Version
		transaction, err := resources.NewTransaction(app.layout.Staging, root, transactionID)
		if err == nil {
			err = transaction.Prepare()
		}
		changes := state.Manifest
		changes.Files = state.Plan.Changes()
		if err == nil && len(changes.Files) > 0 {
			state.Status = "正在下载到 data/staging；正式文件保持不变"
			publish(id, state, false)
			downloader := resources.NewDownloader()
			downloader.OnProgress = func(progress resources.Progress) {
				state.Progress = progress
				publish(id, state, false)
			}
			err = downloader.Download(ctx, changes, transaction.StagingRoot)
		}
		if err == nil && state.PreDownload {
			err = transaction.MarkPreloaded()
			state.Busy = false
			if err != nil {
				state.Status, state.Error = "预下载保存失败；正式文件未修改", err.Error()
			} else {
				state.HasPlan = false
				state.Status = "预下载已完整校验并保存在 data/staging；正式文件未修改"
			}
			publish(id, state, false)
			return
		}
		if err == nil {
			metadata, metadataErr := resources.StageVersionMetadata(root, transaction.StagingRoot, state.Version)
			if metadataErr != nil {
				err = metadataErr
			} else {
				state.Plan.Items = append(state.Plan.Items, metadata...)
			}
		}
		if err == nil {
			state.Status = "下载已完整校验，正在提交事务…"
			publish(id, state, false)
			err = transaction.Commit(state.Plan)
		}
		state.Busy = false
		if err != nil {
			if errors.Is(err, context.Canceled) {
				state.Status, state.Error = "资源任务已取消；正式文件未修改", ""
			} else {
				state.Status = "资源事务失败；已保留或恢复原文件"
				state.Error = err.Error()
			}
			publish(id, state, false)
			return
		}
		state.HasPlan = false
		state.Status = "资源事务完成，全部目标文件已再次校验"
		publish(id, state, true)
	})
}

func (app *application) paintResources(dc win32.HDC, client win32.Rect, left int32) {
	cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
	defer win32.DeleteObject(uintptr(cardBrush))
	buttonBrush := win32.CreateSolidBrush(win32.Color(35, 40, 54))
	defer win32.DeleteObject(uintptr(buttonBrush))
	accentBrush := win32.CreateSolidBrush(win32.Color(52, 66, 112))
	defer win32.DeleteObject(uintptr(accentBrush))
	right := client.Right - win32.Scale(42, app.dpi)
	row := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		win32.FillRect(dc, &rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	state := app.resourceState
	checkText := "检查在线资源并生成修复计划"
	if state.Busy {
		checkText = "取消当前资源任务"
	}
	draw(checkText, row(170, 214, accentBrush), win32.Color(235, 238, 248))
	draw("语音包："+state.Language+"    单击切换", row(220, 258, buttonBrush), win32.Color(225, 229, 242))
	branch := "当前正式版本"
	if state.PreDownload {
		branch = "预下载版本（仅官方开放时可用）"
	}
	draw("资源分支："+branch+"    单击切换", row(264, 302, buttonBrush), win32.Color(225, 229, 242))
	applyText := "下载并修复（需要先生成计划）"
	if state.HasPlan {
		applyText = fmt.Sprintf("下载并修复 %d 个文件", len(state.Plan.Changes()))
	}
	if state.Confirm {
		applyText = "再次单击确认执行；原文件将在事务中备份"
	}
	draw(applyText, row(308, 352, buttonBrush), win32.Color(225, 229, 242))
	statusColor := win32.Color(166, 174, 197)
	status := state.Status
	if state.Error != "" {
		status, statusColor = state.Error, win32.Color(255, 126, 126)
	}
	draw(status, row(364, 408, cardBrush), statusColor)
	version := valueOrUnknown(state.Version)
	draw("在线版本："+version, row(414, 450, cardBrush), win32.Color(190, 197, 216))
	if state.HasPlan {
		draw(fmt.Sprintf("清单 %d 个文件 · 需处理 %d 个 · 下载量 %s", len(state.Manifest.Files), len(state.Plan.Changes()), formatBytes(uint64(state.Plan.DownloadBytes))), row(456, 492, cardBrush), win32.Color(190, 197, 216))
	}
	if state.Progress.FilesTotal > 0 {
		eta := "计算中"
		if state.Progress.ETA > 0 {
			eta = state.Progress.ETA.Round(time.Second).String()
		}
		draw(fmt.Sprintf("%d/%d 文件 · %s/%s · %s/s · 剩余 %s", state.Progress.FilesDone, state.Progress.FilesTotal, formatBytes(uint64(state.Progress.BytesDone)), formatBytes(uint64(state.Progress.BytesTotal)), formatBytes(uint64(state.Progress.Speed)), eta), row(498, 534, cardBrush), win32.Color(145, 154, 180))
	}
	draw("安全边界：下载仅写入 data/staging；完整校验后才进入短暂提交阶段。", row(540, 576, cardBrush), win32.Color(126, 136, 160))
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "未知"
	}
	return value
}

func formatBytes(value uint64) string {
	const (
		kiB = 1024
		miB = 1024 * kiB
		giB = 1024 * miB
	)
	switch {
	case value >= giB:
		return fmt.Sprintf("%.2f GiB", float64(value)/giB)
	case value >= miB:
		return fmt.Sprintf("%.2f MiB", float64(value)/miB)
	case value >= kiB:
		return fmt.Sprintf("%.2f KiB", float64(value)/kiB)
	default:
		return fmt.Sprintf("%d B", value)
	}
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
