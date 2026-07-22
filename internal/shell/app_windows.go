// Package shell implements the stable S02 Windows shell and lifetime boundary.
package shell

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"genshintools/internal/buildinfo"
	"genshintools/internal/capture"
	"genshintools/internal/config"
	"genshintools/internal/cpumonitor"
	"genshintools/internal/diagnostics"
	"genshintools/internal/game"
	"genshintools/internal/gamewindow"
	"genshintools/internal/injection"
	"genshintools/internal/input"
	"genshintools/internal/launch"
	"genshintools/internal/localenhance"
	"genshintools/internal/localization"
	"genshintools/internal/overlay"
	"genshintools/internal/paths"
	"genshintools/internal/platform/win32"
	"genshintools/internal/plugins"
	"genshintools/internal/resources"
	"genshintools/internal/selfupdate"
	"genshintools/internal/shellconfig"
	"genshintools/internal/taskrunner"
	"genshintools/internal/uitheme"
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
	messageDiagnostics  = win32.WM_APP + 15
	messageUpdate       = win32.WM_APP + 16

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
	texts       localization.Catalog
	palette     uitheme.Palette
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

	buttonRects     []win32.Rect
	hoverButton     int
	buttonShadow    win32.HBRUSH
	buttonPen       uintptr
	buttonHoverPen  uintptr
	buttonShadowPen uintptr
	pointerX        int32
	pointerY        int32
	pointerInside   bool
	trackingMouse   bool

	snapshots               chan diagnosticSnapshot
	lastSnap                diagnosticSnapshot
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
	storeSelected           string
	pluginUpdates           chan pluginUpdate
	pluginTask              uint64
	pluginBusy              bool
	pluginDeleteConfirm     string
	pluginCatalog           plugins.Catalog
	pluginCatalogPage       plugins.CatalogPage
	customArgumentsEdit     win32.HWND
	pluginAliasEdit         win32.HWND
	pluginTokenEdit         win32.HWND
	pluginSearchEdit        win32.HWND
	listScrollbar           win32.HWND
	fufuTarget              plugins.FufuTargetConfig
	fufuTargetInstalled     bool
	fufuTargetEnabled       bool
	fufuEdits               map[string]win32.HWND
	fufuEditFields          map[uint16]string
	fufuValues              map[string]string
	fufuScroll              int
	pluginListScroll        int
	storeListScroll         int
	pluginTargetMode        bool
	shellStatus             string
	diagnosticUpdates       chan diagnosticUpdate
	diagnosticBusy          bool
	updateUpdates           chan updateCheckUpdate
	updateTask              uint64
	updateBusy              bool
	updateRelease           *selfupdate.Release
	cpuWarning              atomic.Pointer[cpuWarningConfig]
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
	Committing     bool
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
	target  bool
}

type updateCheckUpdate struct {
	taskID  uint64
	apply   bool
	release *selfupdate.Release
	err     error
}

type diagnosticSnapshot struct {
	Resources  win32.ResourceSnapshot
	CPUPercent float64
	CPUValid   bool
	CPUAlert   bool
}

type diagnosticUpdate struct {
	path string
	err  error
}

type cpuWarningConfig struct {
	Enabled    bool
	Threshold  float64
	DurationMS int
}

var navigation = []struct{ titleKey, subtitleKey string }{
	{"nav.home", "nav.home.subtitle"},
	{"nav.game", "nav.game.subtitle"},
	{"nav.resources", "nav.resources.subtitle"},
	{"nav.server", "nav.server.subtitle"},
	{"nav.local", "nav.local.subtitle"},
	{"nav.capture", "nav.capture.subtitle"},
	{"nav.input", "nav.input.subtitle"},
	{"nav.injection", "nav.injection.subtitle"},
	{"nav.plugins", "nav.plugins.subtitle"},
	{"nav.pluginStore", "nav.pluginStore.subtitle"},
	{"nav.settings", "nav.settings.subtitle"},
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
	texts := localization.New(localization.Language(loaded.Settings.Shell.Language), win32.UserDefaultLocaleName())
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
	pluginStatus := fmt.Sprintf(texts.Text("plugin.status.initialDiscovered"), len(pluginItems))
	if cached, cacheErr := plugins.LoadCatalog(pluginLayout.Catalog); cacheErr == nil {
		if page, queryErr := plugins.QueryCatalog(cached, loaded.Settings.Plugins); queryErr == nil {
			pluginCatalog, pluginCatalogPage = cached, page
			pluginStatus = fmt.Sprintf(texts.Text("plugin.status.cacheLoaded"), page.Total)
		}
	} else if !errors.Is(cacheErr, os.ErrNotExist) {
		pluginStatus = texts.Text("plugin.status.cacheInvalid")
	}
	app := &application{
		instance:   win32.ModuleHandle(),
		settings:   loaded.Settings,
		texts:      texts,
		lastBounds: loaded.Settings.Window,
		layout:     layout,
		build:      build,
		logger:     logger,
		tasks: taskrunner.New(func(value any) {
			logger.Panic("panic in background task", value)
		}),
		previousBad:         previousBad,
		recovered:           loaded.RecoveredFrom,
		snapshots:           make(chan diagnosticSnapshot, 1),
		inputUpdates:        make(chan input.Snapshot, 1),
		physicalEvents:      make(chan input.PhysicalEvent, 16),
		gameUpdates:         make(chan gameUpdate, 1),
		launchUpdates:       make(chan launch.Snapshot, 1),
		resourceUpdates:     make(chan resourceUpdate, 1),
		resourceState:       resourceViewState{Language: "zh-cn"},
		serverUpdates:       make(chan serverUpdate, 1),
		serverState:         serverViewState{Target: localenhance.QuickOfficial, AdvancedTarget: localenhance.AdvancedGlobal},
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
		diagnosticUpdates:   make(chan diagnosticUpdate, 1),
		updateUpdates:       make(chan updateCheckUpdate, 1),
		shellStatus:         "",
		captureStatus:       "",
		overlayStatus:       "",
		injectionStatus:     "",
		pluginTargetMode:    true,
		hoverButton:         -1,
	}
	app.syncStoreSelection()
	app.refreshFufuTarget()
	app.shellStatus = app.texts.Text("settings.status.ready")
	app.resourceState.Status = app.texts.Text("resource.status.ready")
	app.serverState.Status = app.texts.Text("server.status.ready")
	app.captureStatus = app.texts.Text("media.status.captureDisabled")
	app.overlayStatus = app.texts.Text("media.status.overlayDisabled")
	app.injectionStatus = app.texts.Text("injection.status.ready")
	app.publishCPUWarningConfig()
	app.palette = uitheme.Current(app.settings.Shell.Theme)
	win32.SetColorTransform(app.palette.Map)
	active = app
	defer func() { active = nil }()

	logger.Info("application starting", map[string]any{"version": build.Version, "commit": build.Commit, "previousUncleanExit": previousBad})
	if loaded.RecoveredFrom != "" {
		logger.Error("corrupt settings quarantined", map[string]any{"path": loaded.RecoveredFrom})
	}
	if err := app.applyProcessPriority(); err != nil {
		app.shellStatus = fmt.Sprintf(app.texts.Text("settings.status.priorityFailed"), err)
		logger.Error("apply process priority", map[string]any{"error": err.Error()})
	}
	if pluginLoad.RecoveredFrom != "" {
		logger.Error("corrupt plugin state quarantined", map[string]any{"path": pluginLoad.RecoveredFrom})
		app.pluginStatus = app.texts.Text("plugin.status.stateRecovered")
	}
	if err := resources.RecoverTransactions(layout.Staging); err != nil {
		logger.Error("resource transaction recovery", map[string]any{"error": err.Error()})
		app.resourceState.Error = app.texts.Text("resource.status.recoveryFailed")
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
		Style:     0x0008,
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

	windowSettings := app.settings.Window
	if !app.settings.Shell.RememberWindowSize {
		windowSettings = config.Default().Window
	}
	bounds := clampBounds(windowSettings)
	app.lastBounds = bounds
	title := win32.UTF16(app.texts.Text("app.title") + " " + app.build.Version)
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
	win32.SetDarkTitleBar(hwnd, app.palette.Dark)
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
			if app.lastSnap.CPUAlert {
				app.shellStatus = fmt.Sprintf("%s %.1f%%", app.texts.Text("settings.cpuWarning.title"), app.lastSnap.CPUPercent)
				if app.trayAdded {
					win32.ShowTrayWarning(app.tray, app.texts.Text("settings.cpuWarning.title"), app.texts.Text("settings.cpuWarning.message"))
				}
				app.logger.Error("sustained launcher CPU usage", map[string]any{"percent": app.lastSnap.CPUPercent, "threshold": app.settings.Shell.CPUWarningThreshold, "durationMs": app.settings.Shell.CPUWarningDurationMS})
			}
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
						app.injectionStatus = fmt.Sprintf(app.texts.Text("injection.status.launchSuccess"), app.launchSnap.PID)
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
					app.captureStatus = fmt.Sprintf(app.texts.Text("media.status.captureFailed"), result.Error)
					app.logger.Error("capture game window", map[string]any{"error": result.Error})
				} else {
					app.captureStatus = fmt.Sprintf(app.texts.Text("media.status.captureSaved"), result.Path)
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
						app.overlayStatus = fmt.Sprintf(app.texts.Text("media.status.overlayFailed"), update.err)
					} else if update.session != nil {
						app.overlayStatus = fmt.Sprintf(app.texts.Text("media.status.overlayRunning"), update.target.PID)
					} else {
						app.overlayStatus = app.texts.Text("media.status.overlayStopped")
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
							next := app.settings.Injection
							next.ModuleID = ""
							if len(update.modules) > 0 {
								next.ModuleID = update.modules[0].Manifest.ID
							}
							if !app.commitInjectionSettings(next) {
								update.status = ""
							}
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
						app.logger.Error("plugin task failed", map[string]any{"error": update.err})
					} else {
						if update.state != nil {
							app.pluginState = *update.state
						}
						if update.catalog != nil {
							app.pluginCatalog = *update.catalog
							app.pluginCatalogPage = update.page
							app.storeListScroll = 0
							app.syncStoreSelection()
						}
						app.refreshPlugins()
						app.pluginStatus = update.status
						if update.status != "" {
							app.logger.Info("plugin task completed", map[string]any{"status": update.status})
						}
					}
					if update.target {
						app.refreshFufuTarget()
						app.injectionStatus = app.pluginStatus
					}
				}
			default:
				win32.Invalidate(hwnd)
				return 0
			}
		}
	case messageDiagnostics:
		select {
		case update := <-app.diagnosticUpdates:
			app.diagnosticBusy = false
			if update.err != nil {
				app.shellStatus = app.texts.Text("settings.status.exportFailed") + update.err.Error()
				app.logger.Error("export diagnostics", map[string]any{"error": update.err.Error()})
			} else {
				app.shellStatus = app.texts.Text("settings.status.exported") + update.path
				app.logger.Info("diagnostics exported", map[string]any{"path": update.path})
			}
		default:
		}
		win32.Invalidate(hwnd)
		return 0
	case messageUpdate:
		for {
			select {
			case update := <-app.updateUpdates:
				if update.taskID != app.updateTask {
					continue
				}
				app.updateBusy = false
				if update.apply {
					if update.err != nil {
						app.shellStatus = app.updateStatus("update failed: ", "更新失败：") + update.err.Error()
						app.logger.Error("apply application update", map[string]any{"error": update.err.Error()})
					} else {
						app.shellStatus = app.updateStatus("update prepared; restarting...", "更新已准备，正在重启……")
						win32.PostMessage(app.hwnd, win32.WM_CLOSE, 0, 0)
					}
					app.updateRelease = nil
				} else if update.err != nil {
					app.updateRelease = nil
					app.shellStatus = app.updateStatus("update check failed: ", "更新检查失败：") + update.err.Error()
					app.logger.Error("check application update", map[string]any{"error": update.err.Error()})
				} else if update.release != nil {
					release := *update.release
					app.updateRelease = &release
					app.shellStatus = app.updateStatus("update available: v", "发现新版本：v") + release.Manifest.Version
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
	case win32.WM_MOUSEMOVE:
		x, y := int32(int16(lParam&0xffff)), int32(int16((lParam>>16)&0xffff))
		app.pointerX, app.pointerY, app.pointerInside = x, y, true
		if !app.trackingMouse {
			app.trackingMouse = win32.TrackMouseLeave(hwnd)
		}
		hovered := buttonIndexAt(app.buttonRects, x, y)
		if hovered != app.hoverButton {
			previous := app.hoverButton
			app.hoverButton = hovered
			app.invalidateButtonHover(previous, hovered)
		}
		return 0
	case win32.WM_MOUSELEAVE:
		app.pointerInside = false
		app.trackingMouse = false
		if app.hoverButton != -1 {
			previous := app.hoverButton
			app.hoverButton = -1
			app.invalidateButtonHover(previous, -1)
		}
		return 0
	case win32.WM_LBUTTONDOWN:
		x, y := int(int16(lParam&0xffff)), int(int16((lParam>>16)&0xffff))
		client := win32.GetClientRect(hwnd)
		contentAction := x >= int(win32.Scale(252, app.dpi)) && x < int(client.Right-win32.Scale(42, app.dpi))
		if selected := app.navigationAt(x, y); selected >= 0 && selected != app.selected {
			if app.selected == 8 && app.pluginTargetMode && !app.flushFufuEdits() {
				win32.Invalidate(hwnd)
				return 0
			}
			app.selected = selected
			app.updateLaunchControlVisibility()
			win32.Invalidate(hwnd)
		} else if app.selected == 1 && contentAction {
			app.gameClick(x, y)
		} else if app.selected == 2 && contentAction {
			app.resourceClick(x, y)
		} else if app.selected == 3 && contentAction {
			app.serverClick(x, y)
		} else if app.selected == 4 && contentAction {
			app.localEnhanceClick(x, y)
		} else if app.selected == 5 && contentAction {
			app.mediaClick(x, y)
		} else if app.selected == 6 && contentAction {
			app.inputClick(x, y)
		} else if app.selected == 7 && contentAction {
			app.injectionClick(x, y)
		} else if app.selected == 8 && contentAction {
			app.pluginClick(x, y)
		} else if app.selected == 9 && contentAction {
			app.pluginStoreClick(x, y)
		} else if app.selected == 10 && contentAction {
			app.settingsClick(y)
		}
		return 0
	case win32.WM_MOUSEWHEEL:
		if app.selected == 8 || app.selected == 9 {
			delta := int16((wParam >> 16) & 0xffff)
			if delta != 0 {
				step := 3
				if delta > 0 {
					step = -3
				}
				app.scrollActiveList(step)
			}
			return 0
		}
	case win32.WM_VSCROLL:
		if win32.HWND(lParam) == app.listScrollbar {
			app.handleListScroll(uint16(wParam & 0xffff))
			return 0
		}
	case win32.WM_HOTKEY:
		if int32(wParam) == captureHotkeyID && app.captureManager != nil {
			if app.captureManager.Request() {
				app.captureStatus = app.texts.Text("media.status.requestQueued")
			} else {
				app.captureStatus = app.texts.Text("media.status.requestRejected")
			}
			win32.Invalidate(hwnd)
		}
		return 0
	case win32.WM_KEYDOWN:
		switch wParam {
		case win32.VK_UP:
			if app.selected > 0 {
				if app.selected == 8 && app.pluginTargetMode && !app.flushFufuEdits() {
					return 0
				}
				app.selected--
				app.updateLaunchControlVisibility()
				win32.Invalidate(hwnd)
			}
		case win32.VK_DOWN:
			if app.selected < len(navigation)-1 {
				if app.selected == 8 && app.pluginTargetMode && !app.flushFufuEdits() {
					return 0
				}
				app.selected++
				app.updateLaunchControlVisibility()
				win32.Invalidate(hwnd)
			}
		}
		return 0
	case win32.WM_COMMAND:
		controlID := uint16(wParam & 0xffff)
		notification := uint16((wParam >> 16) & 0xffff)
		if controlID == 2004 && notification == 0x0200 {
			app.savePluginSearch()
			win32.Invalidate(hwnd)
		}
		if fieldID, ok := app.fufuEditFields[controlID]; ok && notification == 0x0200 {
			app.saveFufuField(fieldID)
			win32.Invalidate(hwnd)
		}
		return 0
	case win32.WM_CTLCOLOREDIT:
		win32.SetTextColor(win32.HDC(wParam), win32.Color(225, 229, 242))
		win32.SetBackgroundColor(win32.HDC(wParam), win32.Color(25, 29, 39))
		return uintptr(app.editBrush)
	case win32.WM_SYSCOLORCHANGE, win32.WM_SETTINGCHANGE, win32.WM_THEMECHANGED:
		app.refreshTheme()
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
			if app.settings.Shell.MinimizeToTray {
				win32.ShowWindow(hwnd, win32.SW_HIDE)
			}
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
		if app.settings.Shell.EnforceMinimumSize {
			info := (*win32.MinMaxInfo)(unsafe.Pointer(lParam))
			info.MinTrackSize = win32.Point{X: win32.Scale(840, app.dpi), Y: win32.Scale(560, app.dpi)}
		}
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
	win32.CopyUTF16(app.tray.Tip[:], app.texts.Text("app.title")+" "+app.build.Version)
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
		if !app.flushFufuEdits() {
			app.logger.Error("save pending FuFuPlugin settings during shutdown", map[string]any{"error": app.pluginStatus})
		}
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
		app.tasks.Cancel(app.updateTask)
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
		if app.settings.Shell.RememberWindowSize {
			app.settings.Window = app.lastBounds
		}
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
	if app.pluginTokenEdit != 0 {
		win32.SetControlFont(app.pluginTokenEdit, app.fontBody)
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
	startValue, heightValue := app.navigationMetrics()
	start, height := int(startValue), int(heightValue)
	index := (y - start) / height
	if y < start || index < 0 || index >= len(navigation) {
		return -1
	}
	return index
}

func (app *application) navigationMetrics() (start, itemHeight int32) {
	start = win32.Scale(88, app.dpi)
	itemHeight = win32.Scale(48, app.dpi)
	if app.hwnd == 0 || len(navigation) == 0 {
		return start, itemHeight
	}
	client := win32.GetClientRect(app.hwnd)
	bottom := client.Bottom - win32.Scale(34, app.dpi)
	if available := bottom - start; available > 0 && itemHeight*int32(len(navigation)) > available {
		itemHeight = max(win32.Scale(28, app.dpi), available/int32(len(navigation)))
	}
	return start, itemHeight
}

func (app *application) pluginContentY(logical int32) int32 {
	if app.hwnd == 0 {
		return win32.Scale(logical, app.dpi)
	}
	client := win32.GetClientRect(app.hwnd)
	return scalePluginContentY(logical, app.dpi, client.Bottom-win32.Scale(42, app.dpi))
}

func scalePluginContentY(logical int32, dpi uint32, availableBottom int32) int32 {
	designTop := win32.Scale(170, dpi)
	designBottom := win32.Scale(660, dpi)
	value := win32.Scale(logical, dpi)
	if availableBottom >= designBottom || availableBottom <= designTop {
		return value
	}
	return designTop + (value-designTop)*(availableBottom-designTop)/(designBottom-designTop)
}

func (app *application) paint(hwnd win32.HWND) {
	var paint win32.PaintStruct
	windowDC := win32.BeginPaint(hwnd, &paint)
	defer win32.EndPaint(hwnd, &paint)
	client := win32.GetClientRect(hwnd)
	dc := windowDC
	if width, height := client.Right-client.Left, client.Bottom-client.Top; width > 0 && height > 0 {
		memoryDC := win32.CreateCompatibleDC(windowDC)
		if memoryDC != 0 {
			bitmap := win32.CreateCompatibleBitmap(windowDC, width, height)
			if bitmap != 0 {
				oldBitmap := win32.SelectObject(memoryDC, bitmap)
				dc = memoryDC
				defer func() {
					dirty := paint.Paint
					if dirty.Right > dirty.Left && dirty.Bottom > dirty.Top {
						win32.BitBlt(windowDC, dirty.Left, dirty.Top, dirty.Right-dirty.Left, dirty.Bottom-dirty.Top, memoryDC, dirty.Left, dirty.Top)
					}
					win32.SelectObject(memoryDC, oldBitmap)
					win32.DeleteObject(bitmap)
					win32.DeleteDC(memoryDC)
				}()
			} else {
				win32.DeleteDC(memoryDC)
			}
		}
	}
	background := win32.CreateSolidBrush(win32.Color(15, 17, 23))
	defer win32.DeleteObject(uintptr(background))
	sidebar := win32.CreateSolidBrush(win32.Color(24, 27, 36))
	defer win32.DeleteObject(uintptr(sidebar))
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
	app.beginButtonPaint()
	defer app.endButtonPaint()

	old := win32.SelectObject(dc, uintptr(app.fontNav))
	win32.SetTextColor(dc, win32.Color(235, 238, 248))
	logo := win32.Rect{Left: win32.Scale(22, app.dpi), Top: win32.Scale(24, app.dpi), Right: sidebarWidth - win32.Scale(12, app.dpi), Bottom: win32.Scale(66, app.dpi)}
	win32.DrawText(dc, "GENSHIN TOOLS", &logo, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE)
	start, itemHeight := app.navigationMetrics()
	for index, item := range navigation {
		top := start + int32(index)*itemHeight
		row := win32.Rect{Left: win32.Scale(10, app.dpi), Top: top, Right: sidebarWidth - win32.Scale(10, app.dpi), Bottom: top + itemHeight - win32.Scale(4, app.dpi)}
		app.paintNavigationSurface(dc, row, index == app.selected)
		if index == app.selected {
			bar := win32.Rect{Left: row.Left, Top: row.Top + win32.Scale(8, app.dpi), Right: row.Left + win32.Scale(3, app.dpi), Bottom: row.Bottom - win32.Scale(8, app.dpi)}
			win32.FillRect(dc, &bar, accent)
		}
		row.Left += win32.Scale(18, app.dpi)
		win32.DrawText(dc, app.texts.Text(item.titleKey), &row, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE)
	}

	contentLeft := sidebarWidth + win32.Scale(42, app.dpi)
	win32.SelectObject(dc, uintptr(app.fontTitle))
	titleRect := win32.Rect{Left: contentLeft, Top: win32.Scale(52, app.dpi), Right: client.Right - win32.Scale(30, app.dpi), Bottom: win32.Scale(104, app.dpi)}
	win32.DrawText(dc, app.texts.Text(navigation[app.selected].titleKey), &titleRect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE)
	win32.SelectObject(dc, uintptr(app.fontBody))
	win32.SetTextColor(dc, win32.Color(166, 174, 197))
	subtitle := app.texts.Text(navigation[app.selected].subtitleKey)
	if app.previousBad {
		subtitle += "  " + app.texts.Text("status.previousUncleanExit")
	}
	if app.recovered != "" {
		subtitle += "  " + app.texts.Text("status.configRecovered")
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
	} else if app.selected == 10 {
		app.paintSettings(dc, client, contentLeft)
	} else {
		cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
		defer win32.DeleteObject(uintptr(cardBrush))
		card := win32.Rect{Left: contentLeft, Top: win32.Scale(184, app.dpi), Right: client.Right - win32.Scale(42, app.dpi), Bottom: win32.Scale(330, app.dpi)}
		win32.DrawRoundedRect(dc, card, cardBrush, win32.Color(126, 136, 160), 1, max(int32(8), win32.Scale(12, app.dpi)))
		win32.SetTextColor(dc, win32.Color(225, 229, 242))
		card.Left += win32.Scale(24, app.dpi)
		card.Top += win32.Scale(18, app.dpi)
		card.Right -= win32.Scale(20, app.dpi)
		card.Bottom = card.Top + win32.Scale(34, app.dpi)
		win32.DrawText(dc, app.texts.Text("home.ready"), &card, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE)
		win32.SetTextColor(dc, win32.Color(145, 154, 180))
		card.Top += win32.Scale(42, app.dpi)
		card.Bottom = card.Top + win32.Scale(32, app.dpi)
		win32.DrawText(dc, app.texts.Text("home.summary"), &card, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
		card.Top += win32.Scale(38, app.dpi)
		card.Bottom = card.Top + win32.Scale(32, app.dpi)
		win32.DrawText(dc, app.texts.Text("home.scope"), &card, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}

	win32.SetTextColor(dc, win32.Color(126, 136, 160))
	win32.SelectObject(dc, uintptr(app.fontBody))
	statusRect.Left += win32.Scale(16, app.dpi)
	cpu := "--"
	if app.lastSnap.CPUValid {
		cpu = fmt.Sprintf("%.1f%%", app.lastSnap.CPUPercent)
	}
	resources := app.lastSnap.Resources
	statusText := fmt.Sprintf("v%s  |  DPI %d  |  CPU %s  |  Goroutines %d  |  Threads %d  |  Handles %d  |  USER %d  |  GDI %d", app.build.Version, app.dpi, cpu, runtime.NumGoroutine(), resources.Threads, resources.Handles, resources.USER, resources.GDI)
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
		app.captureStatus = fmt.Sprintf(app.texts.Text("media.status.hotkeyFailed"), err)
		app.logger.Error("register screenshot hotkey", map[string]any{"error": err.Error()})
	} else if app.settings.Capture.Enabled {
		app.captureStatus = app.texts.Text("media.status.captureWaiting")
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
		return errors.New(app.texts.Text("media.error.hotkeyConflict"))
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
		app.overlayStatus = app.texts.Text("media.status.overlayWaiting")
	} else {
		app.overlayStatus = app.texts.Text("media.status.overlayReconciling")
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

func (app *application) commitCaptureSettings(next capture.Config) bool {
	previous := app.settings
	settings := previous
	settings.Capture = next
	app.settings = settings
	if err := app.applyCaptureHotkey(); err != nil {
		app.settings = previous
		if rollbackErr := app.applyCaptureHotkey(); rollbackErr != nil {
			app.logger.Error("restore capture hotkey after apply failure", map[string]any{"error": rollbackErr.Error()})
		}
		app.captureStatus = fmt.Sprintf(app.texts.Text("media.status.hotkeyFailed"), err)
		return false
	}
	if err := config.Save(app.layout.Config, settings); err != nil {
		app.settings = previous
		if rollbackErr := app.applyCaptureHotkey(); rollbackErr != nil {
			app.logger.Error("restore capture hotkey after save failure", map[string]any{"error": rollbackErr.Error()})
		}
		app.captureStatus = fmt.Sprintf(app.texts.Text("media.status.saveFailed"), err)
		return false
	}
	return true
}

func (app *application) commitOverlaySettings(next overlay.Config) bool {
	settings := app.settings
	settings.Overlay = next
	if err := config.Save(app.layout.Config, settings); err != nil {
		app.overlayStatus = fmt.Sprintf(app.texts.Text("media.status.saveFailed"), err)
		return false
	}
	app.settings = settings
	return true
}

func (app *application) mediaClick(_, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	switch {
	case y >= sy(170) && y < sy(214):
		next := app.settings.Capture
		next.Enabled = !next.Enabled
		if app.commitCaptureSettings(next) {
			if next.Enabled {
				app.captureStatus = fmt.Sprintf(app.texts.Text("media.status.captureEnabled"), next.HotkeyString())
			} else {
				app.captureStatus = app.texts.Text("media.status.captureStopped")
			}
		}
	case y >= sy(220) && y < sy(264):
		next := app.settings.Capture
		presets := []uint32{0x79, 0x78, 0x2C}
		index := 0
		for i, key := range presets {
			if next.VirtualKey == key {
				index = (i + 1) % len(presets)
				break
			}
		}
		for range presets {
			key := presets[index]
			index = (index + 1) % len(presets)
			if key != app.settings.Input.TriggerKey && key != app.settings.Input.OutputKey && key != app.settings.Input.StopKey {
				next.VirtualKey = key
				break
			}
		}
		if app.commitCaptureSettings(next) {
			app.captureStatus = fmt.Sprintf(app.texts.Text("media.status.hotkeyChanged"), next.HotkeyString())
		}
	case y >= sy(270) && y < sy(314):
		if app.captureManager != nil && app.captureManager.Request() {
			app.captureStatus = app.texts.Text("media.status.requestQueued")
		} else {
			app.captureStatus = app.texts.Text("media.status.requestRejected")
		}
	case y >= sy(320) && y < sy(364):
		path, selected, err := win32.SelectFolderWithTitle(app.hwnd, app.settings.Capture.SaveDir, app.texts.Text("media.folder.prompt"))
		if err != nil {
			app.captureStatus = fmt.Sprintf(app.texts.Text("media.status.folderFailed"), err)
		} else if selected {
			next := app.settings.Capture
			next.SaveDir = app.portableCapturePath(path)
			if app.commitCaptureSettings(next) {
				app.captureStatus = fmt.Sprintf(app.texts.Text("media.status.folderSet"), path)
			}
		}
	case y >= sy(370) && y < sy(414):
		next := app.settings.Overlay
		next.Enabled = !next.Enabled
		if app.commitOverlaySettings(next) {
			app.reconcileCaptureOverlay(true)
		}
	case y >= sy(420) && y < sy(464):
		next := app.settings.Overlay
		switch {
		case next.ShowFPS && next.ShowCPU && next.ShowGPU:
			next.ShowCPU, next.ShowGPU = false, false
		case next.ShowFPS && !next.ShowCPU && !next.ShowGPU:
			next.ShowFPS, next.ShowCPU, next.ShowGPU = false, true, true
		default:
			next.ShowFPS, next.ShowCPU, next.ShowGPU = true, true, true
		}
		if app.commitOverlaySettings(next) {
			app.reconcileCaptureOverlay(true)
		}
	case y >= sy(470) && y < sy(514):
		next := app.settings.Overlay
		presets := [][2]int{{16, 16}, {16, 120}, {300, 16}}
		index := 0
		for i, preset := range presets {
			if next.OffsetX == preset[0] && next.OffsetY == preset[1] {
				index = (i + 1) % len(presets)
				break
			}
		}
		next.OffsetX, next.OffsetY = presets[index][0], presets[index][1]
		if app.commitOverlaySettings(next) {
			app.reconcileCaptureOverlay(true)
		}
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
		app.paintButtonSurface(dc, rect, brush)
		return rect
	}
	staticRow := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		app.paintStaticSurface(dc, rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	draw(fmt.Sprintf(app.texts.Text("media.capture"), onOffText(app.texts.Language(), app.settings.Capture.Enabled)), row(170, 214, activeBrush), win32.Color(235, 238, 248))
	draw(fmt.Sprintf(app.texts.Text("media.hotkey"), app.settings.Capture.HotkeyString()), row(220, 264, buttonBrush), win32.Color(225, 229, 242))
	draw(app.texts.Text("media.captureNow"), row(270, 314, buttonBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf(app.texts.Text("media.saveDir"), app.settings.Capture.SaveDir), row(320, 364, buttonBrush), win32.Color(190, 197, 216))
	draw(fmt.Sprintf(app.texts.Text("media.overlay"), onOffText(app.texts.Language(), app.settings.Overlay.Enabled)), row(370, 414, activeBrush), win32.Color(235, 238, 248))
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
	draw(fmt.Sprintf(app.texts.Text("media.metrics"), strings.Join(metrics, " / ")), row(420, 464, buttonBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf(app.texts.Text("media.offset"), app.settings.Overlay.OffsetX, app.settings.Overlay.OffsetY), row(470, 514, buttonBrush), win32.Color(225, 229, 242))
	statsText := fmt.Sprintf(app.texts.Text("media.realtime"), metricValue(app.overlayStats.FPS, app.overlayStats.FPSValid), metricValue(app.overlayStats.CPU, app.overlayStats.CPUValid), metricValue(app.overlayStats.GPU, app.overlayStats.GPUValid))
	draw(statsText, staticRow(526, 566, cardBrush), win32.Color(166, 174, 197))
	status := app.captureStatus + "  |  " + app.overlayStatus
	draw(status, staticRow(572, 612, cardBrush), win32.Color(126, 136, 160))
}

func metricValue(value float64, valid bool) string {
	if !valid {
		return "N/A"
	}
	return fmt.Sprintf("%.1f", value)
}

func (app *application) fufuTargetDirectory() string {
	return filepath.Join(app.pluginLayout.Modules, plugins.FufuMainTargetID)
}

func (app *application) refreshFufuTarget() {
	directory := app.fufuTargetDirectory()
	target, err := plugins.LoadFufuTargetConfig(filepath.Join(directory, "config.ini"))
	if err != nil {
		app.fufuTarget = plugins.FufuTargetConfig{}
		app.fufuTargetInstalled = false
		app.fufuTargetEnabled = false
		app.fufuScroll = 0
		app.rebuildFufuControls()
		return
	}
	enabled, installed, stateErr := plugins.FufuTargetEnabled(directory, target.DLL)
	app.fufuTarget = target
	app.fufuTargetInstalled = installed
	app.fufuTargetEnabled = enabled
	if stateErr != nil {
		app.pluginStatus = stateErr.Error()
	}
	app.fufuScroll = min(app.fufuScroll, max(0, len(target.Settings)-fufuVisibleRows))
	app.rebuildFufuControls()
}

const (
	fufuVisibleRows   = 6
	pluginVisibleRows = 3
	storeVisibleRows  = 3
)

func (app *application) rebuildFufuControls() {
	old := app.fufuEdits
	app.fufuEdits = map[string]win32.HWND{}
	app.fufuEditFields = map[uint16]string{}
	app.fufuValues = map[string]string{}
	for _, control := range old {
		win32.DestroyWindow(control)
	}
	if app.hwnd == 0 || !app.fufuTargetInstalled || len(app.fufuTarget.Settings) == 0 {
		return
	}
	values, err := plugins.ReadConfig(filepath.Join(app.fufuTargetDirectory(), "config.ini"), app.fufuTarget.Schema)
	if err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("fufu.status.configReadFailed"), err)
		return
	}
	app.fufuValues = values
	for index, setting := range app.fufuTarget.Settings {
		if setting.Field.Type == "bool" {
			continue
		}
		controlID := uint16(3000 + index)
		edit, createErr := win32.CreateControl("EDIT", values[setting.Field.ID], win32.WS_CHILD|win32.WS_BORDER|win32.WS_TABSTOP|win32.ES_AUTOHSCROLL, 0, 0, 100, 32, app.hwnd, uintptr(controlID), app.instance)
		if createErr != nil {
			app.pluginStatus = fmt.Sprintf(app.texts.Text("fufu.status.controlFailed"), setting.Field.Name, createErr)
			continue
		}
		app.fufuEdits[setting.Field.ID] = edit
		app.fufuEditFields[controlID] = setting.Field.ID
		win32.SetTextLimit(edit, 4096)
		win32.SetControlFont(edit, app.fontBody)
		win32.SetControlDarkTheme(edit, app.palette.Dark)
		win32.SetCueBanner(edit, setting.Field.Name)
	}
	app.layoutFufuControls()
}

func (app *application) fufuSetting(fieldID string) (plugins.FufuSetting, bool) {
	for _, setting := range app.fufuTarget.Settings {
		if setting.Field.ID == fieldID {
			return setting, true
		}
	}
	return plugins.FufuSetting{}, false
}

func (app *application) saveFufuField(fieldID string) bool {
	setting, ok := app.fufuSetting(fieldID)
	control := app.fufuEdits[fieldID]
	if !ok || control == 0 {
		return true
	}
	value := win32.GetWindowText(control)
	if value == app.fufuValues[fieldID] {
		return true
	}
	if err := plugins.UpdateConfig(filepath.Join(app.fufuTargetDirectory(), "config.ini"), app.fufuTarget.Schema, setting.Field.ID, value); err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("fufu.status.saveFailed"), err)
		app.logger.Error("save Fufu target setting", map[string]any{"field": setting.Field.ID, "error": err.Error()})
		win32.SetWindowText(control, app.fufuValues[fieldID])
		return false
	}
	app.fufuValues[fieldID] = value
	app.pluginStatus = fmt.Sprintf(app.texts.Text("fufu.status.saved"), setting.Field.Name)
	return true
}

func (app *application) flushFufuEdits() bool {
	for _, setting := range app.fufuTarget.Settings {
		if setting.Field.Type != "bool" && !app.saveFufuField(setting.Field.ID) {
			return false
		}
	}
	return true
}

func (app *application) toggleFufuBool(setting plugins.FufuSetting) {
	current := strings.ToLower(strings.TrimSpace(app.fufuValues[setting.Field.ID]))
	value := "1"
	if current == "1" || current == "true" {
		value = "0"
	}
	if err := plugins.UpdateConfig(filepath.Join(app.fufuTargetDirectory(), "config.ini"), app.fufuTarget.Schema, setting.Field.ID, value); err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("fufu.status.saveFailed"), err)
		return
	}
	app.fufuValues[setting.Field.ID] = value
	app.pluginStatus = fmt.Sprintf(app.texts.Text("fufu.status.saved"), setting.Field.Name)
}

func (app *application) scrollFufuSettings(delta int) {
	app.fufuScroll = clampFufuScroll(app.fufuScroll, delta, len(app.fufuTarget.Settings))
	app.layoutFufuControls()
	win32.Invalidate(app.hwnd)
}

func clampFufuScroll(current, delta, total int) int {
	maximum := max(0, total-fufuVisibleRows)
	return max(0, min(maximum, current+delta))
}

func (app *application) activeListState() (position *int, total, visible int, ok bool) {
	switch {
	case app.selected == 8 && app.pluginTargetMode && app.fufuTargetInstalled:
		return &app.fufuScroll, len(app.fufuTarget.Settings), fufuVisibleRows, true
	case app.selected == 8 && !app.pluginTargetMode:
		return &app.pluginListScroll, len(app.pluginItems), pluginVisibleRows, true
	case app.selected == 9:
		return &app.storeListScroll, len(app.pluginCatalogPage.Items), storeVisibleRows, true
	default:
		return nil, 0, 0, false
	}
}

func (app *application) scrollActiveList(delta int) {
	position, total, visible, ok := app.activeListState()
	if !ok {
		return
	}
	maximum := max(0, total-visible)
	*position = max(0, min(maximum, *position+delta))
	app.layoutFufuControls()
	app.syncListScrollbar()
	win32.Invalidate(app.hwnd)
}

func (app *application) handleListScroll(command uint16) {
	position, total, visible, ok := app.activeListState()
	if !ok {
		return
	}
	next := *position
	switch command {
	case win32.SB_LINEUP:
		next--
	case win32.SB_LINEDOWN:
		next++
	case win32.SB_PAGEUP:
		next -= visible
	case win32.SB_PAGEDOWN:
		next += visible
	case win32.SB_TOP:
		next = 0
	case win32.SB_BOTTOM:
		next = total
	case win32.SB_THUMBPOSITION, win32.SB_THUMBTRACK:
		info := win32.ScrollInfo{Mask: win32.SIF_TRACKPOS}
		if win32.GetScrollInfo(app.listScrollbar, &info) {
			next = int(info.TrackPos)
		}
	}
	maximum := max(0, total-visible)
	*position = max(0, min(maximum, next))
	app.layoutFufuControls()
	app.syncListScrollbar()
	win32.Invalidate(app.hwnd)
}

func (app *application) syncListScrollbar() {
	if app.listScrollbar == 0 || app.hwnd == 0 {
		return
	}
	position, total, visible, ok := app.activeListState()
	if !ok || total <= visible {
		win32.ShowWindow(app.listScrollbar, win32.SW_HIDE)
		return
	}
	client := win32.GetClientRect(app.hwnd)
	right := client.Right - win32.Scale(42, app.dpi)
	top, bottom := int32(0), int32(0)
	switch {
	case app.selected == 8 && app.pluginTargetMode:
		top, bottom = app.pluginContentY(235), app.pluginContentY(529)
	case app.selected == 8:
		top, bottom = app.pluginContentY(270), app.pluginContentY(412)
	case app.selected == 9:
		top, bottom = app.pluginContentY(320), app.pluginContentY(462)
	}
	width := win32.Scale(16, app.dpi)
	win32.SetWindowPos(app.listScrollbar, win32.Rect{Left: right - width, Top: top, Right: right, Bottom: bottom}, win32.SWP_NOZORDER)
	info := win32.ScrollInfo{Mask: win32.SIF_RANGE | win32.SIF_PAGE | win32.SIF_POS, Min: 0, Max: int32(total - 1), Page: uint32(visible), Position: int32(*position)}
	win32.SetScrollInfo(app.listScrollbar, &info, true)
	win32.ShowWindow(app.listScrollbar, win32.SW_SHOWNORMAL)
}

func (app *application) startFufuMainRepair() {
	if app.pluginBusy {
		app.injectionStatus = app.texts.Text("plugin.status.taskBusy")
		return
	}
	if app.gameState.Candidate == nil {
		app.injectionStatus = app.texts.Text("plugin.store.status.needGame")
		return
	}
	if !app.flushFufuEdits() {
		app.injectionStatus = app.pluginStatus
		return
	}
	state := plugins.CloneState(app.pluginState)
	layout := app.pluginLayout
	candidate := *app.gameState.Candidate
	texts := app.texts
	app.pluginBusy = true
	app.injectionStatus = app.texts.Text("fufu.status.downloading")
	app.pluginTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		temporary, err := os.CreateTemp(layout.Staging, "fufu-main-*.zip")
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, target: true, err: fmt.Sprintf(texts.Text("fufu.status.repairFailed"), err)})
			return
		}
		packagePath := temporary.Name()
		_ = temporary.Close()
		_ = os.Remove(packagePath)
		defer os.Remove(packagePath)
		hash, _, err := plugins.DownloadFufuMainPackage(ctx, nil, packagePath)
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, target: true, err: fmt.Sprintf(texts.Text("fufu.status.repairFailed"), err)})
			return
		}
		result, err := plugins.InstallFufuMainPackage(ctx, packagePath, layout, candidate, &state)
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, target: true, err: fmt.Sprintf(texts.Text("fufu.status.repairFailed"), err)})
			return
		}
		if err := plugins.SetEnabled(&state, plugins.FufuMainTargetID, true); err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, target: true, err: fmt.Sprintf(texts.Text("fufu.status.repairFailed"), err)})
			return
		}
		if err := plugins.SaveState(layout.State, state); err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, target: true, err: fmt.Sprintf(texts.Text("fufu.status.repairFailed"), err)})
			return
		}
		app.publishPlugin(pluginUpdate{taskID: id, target: true, state: &state, status: fmt.Sprintf(texts.Text("fufu.status.repaired"), result.Manifest.Version, hash[:12])})
	})
}

func (app *application) setFufuInjectionEnabled(enable bool) {
	if !app.fufuTargetInstalled {
		app.injectionStatus = app.texts.Text("fufu.status.notInstalled")
		return
	}
	if !app.flushFufuEdits() {
		app.injectionStatus = app.pluginStatus
		return
	}
	directory := app.fufuTargetDirectory()
	if err := plugins.SetFufuTargetEnabled(directory, app.fufuTarget.DLL, enable); err != nil {
		app.injectionStatus = fmt.Sprintf(app.texts.Text("fufu.status.toggleFailed"), err)
		return
	}
	previous := plugins.CloneState(app.pluginState)
	nextState := plugins.CloneState(app.pluginState)
	if err := plugins.SetEnabled(&nextState, plugins.FufuMainTargetID, enable); err != nil {
		_ = plugins.SetFufuTargetEnabled(directory, app.fufuTarget.DLL, !enable)
		app.injectionStatus = fmt.Sprintf(app.texts.Text("fufu.status.toggleFailed"), err)
		return
	}
	if err := plugins.SaveState(app.pluginLayout.State, nextState); err != nil {
		_ = plugins.SetFufuTargetEnabled(directory, app.fufuTarget.DLL, !enable)
		app.injectionStatus = fmt.Sprintf(app.texts.Text("fufu.status.toggleFailed"), err)
		return
	}
	next := app.settings.Injection
	next.Enabled = enable
	if !app.commitInjectionSettings(next) {
		_ = plugins.SaveState(app.pluginLayout.State, previous)
		_ = plugins.SetFufuTargetEnabled(directory, app.fufuTarget.DLL, !enable)
		return
	}
	app.pluginState = nextState
	app.refreshFufuTarget()
	app.refreshPlugins()
	app.injectionStatus = fmt.Sprintf(app.texts.Text("fufu.status.toggled"), onOffText(app.texts.Language(), enable))
}

func (app *application) commitInjectionSettings(next injection.Config) bool {
	normalized, err := next.Normalized()
	if err != nil {
		app.injectionStatus = fmt.Sprintf(app.texts.Text("injection.status.invalid"), err)
		return false
	}
	settings := app.settings
	settings.Injection = normalized
	if err := config.Save(app.layout.Config, settings); err != nil {
		app.injectionStatus = fmt.Sprintf(app.texts.Text("injection.status.saveFailed"), err)
		return false
	}
	app.settings = settings
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
		app.injectionStatus = app.texts.Text("injection.status.auditNeedGame")
		return
	}
	app.tasks.Cancel(app.injectionAuditTask)
	candidate := *app.gameState.Candidate
	app.injectionStatus = app.texts.Text("injection.status.auditing")
	texts := app.texts
	app.injectionAuditTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		modules, warnings, err := injection.DiscoverCompatible(app.layout.Modules, candidate)
		if ctx.Err() != nil {
			return
		}
		update := injectionUpdate{taskID: id, kind: 0, modules: modules, warnings: warnings}
		if err != nil {
			update.err = fmt.Sprintf(texts.Text("injection.status.auditFailed"), err)
		} else {
			update.status = fmt.Sprintf(texts.Text("injection.status.auditComplete"), len(modules), len(warnings))
		}
		app.publishInjection(update)
	})
}

func (app *application) startInjectionLaunch() {
	if app.gameState.Candidate == nil || app.launchEngine == nil {
		app.injectionStatus = app.texts.Text("injection.status.launchNeedGame")
		return
	}
	if !app.syncLaunchConfig() {
		app.injectionStatus = fmt.Sprintf(app.texts.Text("injection.status.launchConfigFailed"), app.launchUIError)
		return
	}
	if !app.commitInjectionSettings(app.settings.Injection) {
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
		app.injectionStatus = app.texts.Text("injection.status.safeModeBlocked")
		return
	}
	if !settings.Enabled || !settings.RiskAcknowledged || (settings.ModuleID == "" && len(moduleIDs) == 0) {
		app.injectionStatus = app.texts.Text("injection.status.rejected")
		return
	}
	app.tasks.Cancel(app.injectionLaunchTask)
	candidate := *app.gameState.Candidate
	app.injectionStatus = app.texts.Text("injection.status.starting")
	app.injectionLaunching = true
	texts := app.texts
	app.injectionLaunchTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		starter := injection.Starter{Context: ctx, HelperPath: filepath.Join(app.layout.Root, "GenshinTools-injector.exe"), ModulesRoot: app.layout.Modules, StagingRoot: app.layout.Staging, Config: settings, ModuleIDs: moduleIDs}
		err := app.launchEngine.LaunchWithStarter(candidate, launchSettings, starter)
		update := injectionUpdate{taskID: id, kind: 1, status: texts.Text("injection.status.helperDone")}
		if err != nil {
			update.status = ""
			update.err = fmt.Sprintf(texts.Text("injection.status.launchFailed"), err)
		}
		app.publishInjection(update)
	})
}

func (app *application) injectionClick(x, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	switch {
	case y >= sy(170) && y < sy(230):
		client := win32.GetClientRect(app.hwnd)
		_, repair, toggle := fufuHeaderRects(win32.Scale(252, app.dpi), client.Right-win32.Scale(42, app.dpi), win32.Scale(170, app.dpi), win32.Scale(230, app.dpi), app.dpi)
		switch {
		case pointInButton(toggle, int32(x), int32(y)):
			app.setFufuInjectionEnabled(!(app.fufuTargetEnabled && app.settings.Injection.Enabled))
		case pointInButton(repair, int32(x), int32(y)):
			app.startFufuMainRepair()
		}
	case y >= sy(240) && y < sy(284):
		next := app.settings.Injection
		next.RiskAcknowledged = !next.RiskAcknowledged
		app.commitInjectionSettings(next)
	case y >= sy(290) && y < sy(334):
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
		next := app.settings.Injection
		next.ModuleID = app.injectionModules[index].Manifest.ID
		if app.commitInjectionSettings(next) {
			app.injectionStatus = fmt.Sprintf(app.texts.Text("injection.status.moduleSelected"), app.injectionModules[index].Manifest.Name)
		}
	case y >= sy(340) && y < sy(384):
		app.startInjectionAudit()
	case y >= sy(390) && y < sy(434):
		app.startInjectionLaunch()
	case y >= sy(440) && y < sy(484):
		app.injectionLaunching = false
		if app.gameState.Candidate == nil || app.launchEngine == nil {
			app.injectionStatus = app.texts.Text("injection.status.cleanNeedGame")
		} else if !app.syncLaunchConfig() {
			app.injectionStatus = fmt.Sprintf(app.texts.Text("injection.status.launchConfigFailed"), app.launchUIError)
		} else if err := app.launchEngine.Launch(*app.gameState.Candidate, app.settings.Launch); err != nil {
			app.injectionStatus = fmt.Sprintf(app.texts.Text("injection.status.cleanFailed"), err)
		} else {
			app.injectionStatus = app.texts.Text("injection.status.cleanStarted")
		}
	case y >= sy(490) && y < sy(534):
		presets := [][2]int{{15000, 5000}, {30000, 10000}, {60000, 20000}}
		index := 0
		for i, preset := range presets {
			if preset[0] == app.settings.Injection.HelperTimeoutMS && preset[1] == app.settings.Injection.RemoteTimeoutMS {
				index = (i + 1) % len(presets)
				break
			}
		}
		next := app.settings.Injection
		next.HelperTimeoutMS, next.RemoteTimeoutMS = presets[index][0], presets[index][1]
		if app.commitInjectionSettings(next) {
			app.injectionStatus = app.texts.Text("injection.status.timeoutChanged")
		}
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
		app.paintButtonSurface(dc, rect, brush)
		return rect
	}
	staticRow := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		app.paintStaticSurface(dc, rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	settings := app.settings.Injection
	selectorRect, repairRect, toggleRect := fufuHeaderRects(left, right, win32.Scale(170, app.dpi), win32.Scale(230, app.dpi), app.dpi)
	app.paintStaticSurface(dc, selectorRect, cardBrush)
	app.paintButtonSurface(dc, repairRect, buttonBrush)
	app.paintButtonSurface(dc, toggleRect, accentBrush)
	metadata := app.texts.Text("fufu.target.notInstalled")
	if app.fufuTargetInstalled {
		metadata = fmt.Sprintf("%s  |  %s  |  %s", app.fufuTarget.Name, app.fufuTarget.Version, app.fufuTarget.Developer)
	}
	draw(fmt.Sprintf(app.texts.Text("fufu.target.label"), metadata), selectorRect, win32.Color(225, 229, 242))
	draw(app.texts.Text("fufu.target.repair"), repairRect, win32.Color(190, 197, 216))
	toggleText := onOffText(app.texts.Language(), app.fufuTargetEnabled && settings.Enabled)
	draw(fmt.Sprintf(app.texts.Text("fufu.target.injection"), toggleText), toggleRect, win32.Color(235, 238, 248))
	risk := app.texts.Text("injection.risk.unconfirmed")
	if settings.RiskAcknowledged {
		risk = app.texts.Text("injection.risk.confirmed")
	}
	draw(fmt.Sprintf(app.texts.Text("injection.risk"), risk), row(240, 284, warningBrush), win32.Color(255, 205, 150))
	module := app.texts.Text("injection.module.none")
	for _, audit := range app.injectionModules {
		if audit.Manifest.ID == settings.ModuleID {
			module = audit.Manifest.Name + " · " + audit.SHA256[:12]
			break
		}
	}
	draw(fmt.Sprintf(app.texts.Text("injection.module"), module), row(290, 334, buttonBrush), win32.Color(225, 229, 242))
	draw(app.texts.Text("injection.audit"), row(340, 384, buttonBrush), win32.Color(190, 197, 216))
	helperMode := app.texts.Text("injection.helper.current")
	if settings.ElevatedHelper {
		helperMode = app.texts.Text("injection.helper.admin")
	}
	draw(fmt.Sprintf(app.texts.Text("injection.launch"), helperMode), row(390, 434, warningBrush), win32.Color(255, 205, 150))
	draw(app.texts.Text("injection.cleanLaunch"), row(440, 484, accentBrush), win32.Color(235, 238, 248))
	draw(fmt.Sprintf(app.texts.Text("injection.timeout"), float64(settings.HelperTimeoutMS)/1000, float64(settings.RemoteTimeoutMS)/1000), row(490, 534, buttonBrush), win32.Color(190, 197, 216))
	warning := app.texts.Text("injection.warning.none")
	if len(app.injectionWarnings) > 0 {
		warning = app.injectionWarnings[0]
	}
	draw(warning, staticRow(540, 574, cardBrush), win32.Color(166, 174, 197))
	draw(app.injectionStatus, staticRow(580, 614, cardBrush), win32.Color(145, 154, 180))
}

func (app *application) commitPluginSettings(next plugins.Config) bool {
	normalized, err := next.Normalized()
	if err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.invalid"), err)
		return false
	}
	settings := app.settings
	settings.Plugins = normalized
	if err := config.Save(app.layout.Config, settings); err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.saveFailed"), err)
		return false
	}
	app.settings = settings
	return true
}

func (app *application) refreshPlugins() bool {
	items, warnings, err := plugins.Discover(app.layout.Modules, app.pluginState)
	if err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.discoverFailed"), err)
		return false
	}
	app.pluginItems, app.pluginWarnings = items, warnings
	app.pluginListScroll = min(app.pluginListScroll, max(0, len(items)-pluginVisibleRows))
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
	app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.discovered"), len(items), len(warnings))
	app.syncListScrollbar()
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
	if app.pluginTargetMode {
		app.fufuConfigClick(x, y)
		win32.Invalidate(app.hwnd)
		return
	}
	sy := func(value int32) int { return int(app.pluginContentY(value)) }
	switch {
	case y >= sy(170) && y < sy(214):
		client := win32.GetClientRect(app.hwnd)
		safe, target := pluginHeaderRects(win32.Scale(252, app.dpi), client.Right-win32.Scale(42, app.dpi), app.pluginContentY(170), app.pluginContentY(214), app.dpi)
		if pointInButton(target, int32(x), int32(y)) {
			app.pluginTargetMode = true
			app.updateLaunchControlVisibility()
			break
		}
		if !pointInButton(safe, int32(x), int32(y)) {
			break
		}
		next := app.settings.Plugins
		next.SafeMode = !next.SafeMode
		if app.commitPluginSettings(next) {
			if next.SafeMode {
				app.pluginStatus = app.texts.Text("plugin.status.safeOn")
			} else {
				app.pluginStatus = app.texts.Text("plugin.status.safeOff")
			}
		}
	case y >= sy(220) && y < sy(264):
		client := win32.GetClientRect(app.hwnd)
		row := win32.Rect{Left: win32.Scale(252, app.dpi), Top: app.pluginContentY(220), Right: client.Right - win32.Scale(42, app.dpi), Bottom: app.pluginContentY(264)}
		switch buttonCellAt(row, int32(x), int32(y), 2, app.dpi) {
		case 0:
			app.refreshPlugins()
			app.startPluginCatalogSync()
		case 1:
			app.startLocalPluginInstall()
		}
	case y >= sy(270) && y < sy(412):
		for visible := 0; visible < pluginVisibleRows; visible++ {
			index := app.pluginListScroll + visible
			if index >= len(app.pluginItems) {
				break
			}
			top := int32(270 + visible*50)
			if y >= sy(top) && y < sy(top+42) {
				app.pluginSelected = app.pluginItems[index].Manifest.ID
				app.pluginDeleteConfirm = ""
				app.syncPluginAliasEdit()
				app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.selected"), app.pluginItems[index].DisplayName())
				break
			}
		}
	case y >= sy(420) && y < sy(464):
		item, ok := app.selectedPlugin()
		if !ok {
			app.pluginStatus = app.texts.Text("plugin.status.selectFirst")
			break
		}
		next := plugins.CloneState(app.pluginState)
		if err := plugins.SetEnabled(&next, item.Manifest.ID, !item.Enabled); err != nil {
			app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.toggleFailed"), err)
		} else if err := plugins.SaveState(app.pluginLayout.State, next); err != nil {
			app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.stateSaveFailed"), err)
		} else {
			app.pluginState = next
			app.refreshPlugins()
		}
	case y >= sy(470) && y < sy(514):
		// The child edit control owns this row.
	case y >= sy(520) && y < sy(564):
		client := win32.GetClientRect(app.hwnd)
		row := win32.Rect{Left: win32.Scale(252, app.dpi), Top: app.pluginContentY(520), Right: client.Right - win32.Scale(42, app.dpi), Bottom: app.pluginContentY(564)}
		switch buttonCellAt(row, int32(x), int32(y), 3, app.dpi) {
		case 0:
			app.savePluginAlias()
		case 1:
			app.movePlugin(-1)
		case 2:
			app.movePlugin(1)
		}
	case y >= sy(570) && y < sy(614):
		client := win32.GetClientRect(app.hwnd)
		row := win32.Rect{Left: win32.Scale(252, app.dpi), Top: app.pluginContentY(570), Right: client.Right - win32.Scale(42, app.dpi), Bottom: app.pluginContentY(614)}
		switch buttonCellAt(row, int32(x), int32(y), 3, app.dpi) {
		case 0:
			app.startPluginRollback()
		case 1:
			app.startPluginUninstall()
		case 2:
			app.applyNextPluginPreset()
		}
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) fufuConfigClick(x, y int) {
	sy := func(value int32) int { return int(app.pluginContentY(value)) }
	switch {
	case y >= sy(170) && y < sy(224):
		client := win32.GetClientRect(app.hwnd)
		_, repair, toggle := fufuHeaderRects(win32.Scale(252, app.dpi), client.Right-win32.Scale(42, app.dpi), app.pluginContentY(170), app.pluginContentY(224), app.dpi)
		switch {
		case pointInButton(toggle, int32(x), int32(y)):
			app.setFufuInjectionEnabled(!(app.fufuTargetEnabled && app.settings.Injection.Enabled))
		case pointInButton(repair, int32(x), int32(y)):
			app.startFufuMainRepair()
		}
	case y >= sy(235) && y < sy(535):
		for visible := 0; visible < fufuVisibleRows; visible++ {
			index := app.fufuScroll + visible
			if index >= len(app.fufuTarget.Settings) {
				break
			}
			top := int32(235 + visible*50)
			if y >= sy(top) && y < sy(top+42) && app.fufuTarget.Settings[index].Field.Type == "bool" {
				app.toggleFufuBool(app.fufuTarget.Settings[index])
				break
			}
		}
	case y >= sy(540) && y < sy(584):
		if !app.flushFufuEdits() {
			break
		}
		app.pluginTargetMode = false
		app.updateLaunchControlVisibility()
	}
}

func (app *application) savePluginAlias() {
	item, ok := app.selectedPlugin()
	if !ok {
		app.pluginStatus = app.texts.Text("plugin.status.selectFirst")
		return
	}
	alias := win32.GetWindowText(app.pluginAliasEdit)
	next := plugins.CloneState(app.pluginState)
	if err := plugins.SetAlias(&next, item.Manifest.ID, alias); err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.aliasInvalid"), err)
		return
	}
	if err := plugins.SaveState(app.pluginLayout.State, next); err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.aliasSaveFailed"), err)
		return
	}
	app.pluginState = next
	app.refreshPlugins()
	app.pluginStatus = app.texts.Text("plugin.status.aliasSaved")
}

func (app *application) startPluginCatalogSync() {
	if app.pluginBusy {
		return
	}
	configCopy := app.settings.Plugins
	destination := app.pluginLayout.Catalog
	app.pluginBusy = true
	app.pluginStatus = app.texts.Text("plugin.store.status.syncing")
	texts := app.texts
	app.pluginTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		catalog, err := plugins.SyncCatalog(ctx, nil, destination)
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: fmt.Sprintf(texts.Text("plugin.store.status.syncFailed"), err)})
			return
		}
		page, err := plugins.QueryCatalog(catalog, configCopy)
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: fmt.Sprintf(texts.Text("plugin.store.status.queryFailed"), err)})
			return
		}
		app.publishPlugin(pluginUpdate{taskID: id, catalog: &catalog, page: page, status: fmt.Sprintf(texts.Text("plugin.store.status.synced"), page.Total, page.Page, page.TotalPages)})
	})
}

func (app *application) savePluginSearch() bool {
	if app.pluginSearchEdit == 0 {
		return false
	}
	if app.pluginBusy {
		app.pluginStatus = app.texts.Text("plugin.status.taskBusy")
		win32.SetWindowText(app.pluginSearchEdit, app.settings.Plugins.Search)
		return false
	}
	next := app.settings.Plugins
	next.Search = strings.TrimSpace(win32.GetWindowText(app.pluginSearchEdit))
	next.Page = 1
	return app.applyPluginStoreConfig(next)
}

func (app *application) applyPluginStoreConfig(next plugins.Config) bool {
	if app.pluginBusy {
		app.pluginStatus = app.texts.Text("plugin.status.taskBusy")
		return false
	}
	normalized, err := next.Normalized()
	if err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.store.status.filterInvalid"), err)
		return false
	}
	page := app.pluginCatalogPage
	if app.pluginCatalog.SchemaVersion != 0 {
		page, err = plugins.QueryCatalog(app.pluginCatalog, normalized)
		if err != nil {
			app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.store.status.filterFailed"), err)
			return false
		}
		normalized.Page = page.Page
	}
	settings := app.settings
	settings.Plugins = normalized
	if err := config.Save(app.layout.Config, settings); err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.store.status.saveFailed"), err)
		return false
	}
	app.settings = settings
	app.pluginCatalogPage = page
	app.storeListScroll = 0
	app.syncStoreSelection()
	app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.store.status.filterUpdated"), page.Total)
	app.syncListScrollbar()
	return true
}

func (app *application) syncStoreSelection() {
	for _, item := range app.pluginCatalogPage.Items {
		if item.ID == app.storeSelected {
			return
		}
	}
	app.storeSelected = ""
	if len(app.pluginCatalogPage.Items) > 0 {
		app.storeSelected = app.pluginCatalogPage.Items[0].ID
	}
}

func (app *application) pluginStoreClick(x, y int) {
	if app.pluginBusy {
		app.pluginStatus = app.texts.Text("plugin.status.taskBusy")
		win32.Invalidate(app.hwnd)
		return
	}
	sy := func(value int32) int { return int(app.pluginContentY(value)) }
	switch {
	case y >= sy(270) && y < sy(314):
		client := win32.GetClientRect(app.hwnd)
		row := win32.Rect{Left: win32.Scale(252, app.dpi), Top: app.pluginContentY(270), Right: client.Right - win32.Scale(42, app.dpi), Bottom: app.pluginContentY(314)}
		switch buttonCellAt(row, int32(x), int32(y), 3, app.dpi) {
		case 0:
			if app.savePluginSearch() {
				app.startPluginCatalogSync()
			}
		case 1:
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
		case 2:
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
		}
	case y >= sy(320) && y < sy(462):
		for visible := 0; visible < storeVisibleRows; visible++ {
			index := app.storeListScroll + visible
			if index >= len(app.pluginCatalogPage.Items) {
				break
			}
			top := int32(320 + visible*50)
			if y >= sy(top) && y < sy(top+42) {
				app.storeSelected = app.pluginCatalogPage.Items[index].ID
				app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.store.status.selected"), app.pluginCatalogPage.Items[index].Name)
				break
			}
		}
	case y >= sy(470) && y < sy(514):
		client := win32.GetClientRect(app.hwnd)
		next := app.settings.Plugins
		row := win32.Rect{Left: win32.Scale(252, app.dpi), Top: app.pluginContentY(470), Right: client.Right - win32.Scale(42, app.dpi), Bottom: app.pluginContentY(514)}
		switch buttonCellAt(row, int32(x), int32(y), 2, app.dpi) {
		case 0:
			if next.Page <= 1 {
				break
			}
			next.Page--
		case 1:
			if next.Page >= app.pluginCatalogPage.TotalPages {
				break
			}
			next.Page++
		default:
			break
		}
		if next.Page != app.settings.Plugins.Page {
			app.applyPluginStoreConfig(next)
		}
	case y >= sy(520) && y < sy(564):
		app.startCatalogPluginInstall()
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) selectedCatalogPlugin() (plugins.CatalogItem, bool) {
	for _, item := range app.pluginCatalogPage.Items {
		if item.ID == app.storeSelected {
			return item, true
		}
	}
	if len(app.pluginCatalogPage.Items) > 0 {
		return app.pluginCatalogPage.Items[0], true
	}
	return plugins.CatalogItem{}, false
}

func (app *application) startCatalogPluginInstall() {
	if app.pluginBusy {
		app.pluginStatus = app.texts.Text("plugin.status.taskBusy")
		return
	}
	item, ok := app.selectedCatalogPlugin()
	if !ok {
		app.pluginStatus = app.texts.Text("plugin.store.status.selectFirst")
		return
	}
	if err := plugins.ValidateFufuDependencies(app.pluginLayout, app.pluginState, item); err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.store.status.installFailed"), err)
		return
	}
	rawToken := ""
	if app.pluginTokenEdit != 0 {
		rawToken = strings.TrimSpace(win32.GetWindowText(app.pluginTokenEdit))
	}
	if rawToken == "" {
		gateURL := plugins.FufuStoreBaseURL + "/plugin-download-gate?id=" + url.QueryEscape(item.ID)
		if err := openExternalURL(gateURL); err != nil {
			app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.store.status.openFufuFailed"), err)
			return
		}
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.store.status.needToken"), item.Name)
		return
	}
	token, err := plugins.ParseFufuDownloadToken(rawToken)
	if err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.store.status.tokenInvalid"), err)
		return
	}
	if app.gameState.Candidate == nil {
		app.pluginStatus = app.texts.Text("plugin.store.status.needGame")
		return
	}
	win32.SetWindowText(app.pluginTokenEdit, "")
	state := plugins.CloneState(app.pluginState)
	candidate := *app.gameState.Candidate
	layout := app.pluginLayout
	app.pluginBusy = true
	app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.store.status.downloading"), item.Name)
	texts := app.texts
	app.pluginTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		temporary, createErr := os.CreateTemp(layout.Staging, strings.ToLower(item.ID)+"-fufu-*.zip")
		if createErr != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: fmt.Sprintf(texts.Text("plugin.store.status.stagingFailed"), createErr)})
			return
		}
		packagePath := temporary.Name()
		_ = temporary.Close()
		_ = os.Remove(packagePath)
		defer os.Remove(packagePath)
		if downloadErr := plugins.DownloadFufuPackage(ctx, nil, item, token, packagePath); downloadErr != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: fmt.Sprintf(texts.Text("plugin.store.status.downloadFailed"), downloadErr)})
			return
		}
		result, installErr := plugins.InstallFufuPackage(ctx, packagePath, item, layout, candidate, &state)
		if installErr != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: fmt.Sprintf(texts.Text("plugin.store.status.installFailed"), installErr)})
			return
		}
		status := fmt.Sprintf(texts.Text("plugin.store.status.installed"), result.Manifest.Name, result.Manifest.Version)
		if result.RollbackReady {
			status += texts.Text("plugin.status.rollbackRetained")
		}
		app.publishPlugin(pluginUpdate{taskID: id, state: &state, status: status})
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
		app.pluginStatus = app.texts.Text("plugin.status.taskBusy")
		return
	}
	if app.gameState.Candidate == nil {
		app.pluginStatus = app.texts.Text("plugin.status.needGame")
		return
	}
	packagePath, selected, err := win32.SelectPluginPackageWithTitle(app.hwnd, app.layout.Root, app.texts.Text("plugin.package.prompt"))
	if err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.packageSelectFailed"), err)
		return
	}
	if !selected {
		return
	}
	state := plugins.CloneState(app.pluginState)
	candidate := *app.gameState.Candidate
	layout := app.pluginLayout
	app.pluginBusy = true
	app.pluginStatus = app.texts.Text("plugin.status.auditingPackage")
	texts := app.texts
	app.pluginTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		item, inspectErr := plugins.InspectLocalPackage(packagePath)
		if inspectErr != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: fmt.Sprintf(texts.Text("plugin.status.packageReadFailed"), inspectErr)})
			return
		}
		result, installErr := plugins.InstallLocalPackage(ctx, packagePath, item, layout, candidate, &state)
		if installErr != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: fmt.Sprintf(texts.Text("plugin.status.installFailed"), installErr)})
			return
		}
		status := fmt.Sprintf(texts.Text("plugin.status.installed"), result.Manifest.Name, result.Manifest.Version)
		if result.RollbackReady {
			status += texts.Text("plugin.status.rollbackRetained")
		}
		app.publishPlugin(pluginUpdate{taskID: id, state: &state, status: status})
	})
}

func (app *application) startPluginRollback() {
	if app.pluginBusy {
		app.pluginStatus = app.texts.Text("plugin.status.taskBusy")
		return
	}
	item, ok := app.selectedPlugin()
	if !ok || app.gameState.Candidate == nil {
		app.pluginStatus = app.texts.Text("plugin.status.rollbackNeedPlugin")
		return
	}
	installed, ok := app.pluginState.Installed[item.Manifest.ID]
	if !ok || len(installed.RollbackVersions) == 0 {
		app.pluginStatus = app.texts.Text("plugin.status.noRollback")
		return
	}
	version := installed.RollbackVersions[len(installed.RollbackVersions)-1]
	state := plugins.CloneState(app.pluginState)
	candidate := *app.gameState.Candidate
	layout := app.pluginLayout
	app.pluginBusy = true
	app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.rollingBack"), version)
	texts := app.texts
	app.pluginTask = app.tasks.Run(func(ctx context.Context, id uint64) {
		result, err := plugins.Rollback(ctx, layout, &state, item.Manifest.ID, version, candidate)
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: fmt.Sprintf(texts.Text("plugin.status.rollbackFailed"), err)})
			return
		}
		app.publishPlugin(pluginUpdate{taskID: id, state: &state, status: fmt.Sprintf(texts.Text("plugin.status.rolledBack"), result.Manifest.Version)})
	})
}

func (app *application) startPluginUninstall() {
	if app.pluginBusy {
		app.pluginStatus = app.texts.Text("plugin.status.taskBusy")
		return
	}
	item, ok := app.selectedPlugin()
	if !ok {
		app.pluginStatus = app.texts.Text("plugin.status.selectFirst")
		return
	}
	if app.pluginDeleteConfirm != item.Manifest.ID {
		app.pluginDeleteConfirm = item.Manifest.ID
		app.pluginStatus = app.texts.Text("plugin.status.uninstallConfirm")
		return
	}
	app.pluginDeleteConfirm = ""
	state := plugins.CloneState(app.pluginState)
	layout := app.pluginLayout
	app.pluginBusy = true
	app.pluginStatus = app.texts.Text("plugin.status.uninstalling")
	texts := app.texts
	app.pluginTask = app.tasks.Run(func(_ context.Context, id uint64) {
		manifest, err := plugins.Uninstall(layout, &state, item.Manifest.ID)
		if err != nil {
			app.publishPlugin(pluginUpdate{taskID: id, err: fmt.Sprintf(texts.Text("plugin.status.uninstallFailed"), err)})
			return
		}
		app.publishPlugin(pluginUpdate{taskID: id, state: &state, status: fmt.Sprintf(texts.Text("plugin.status.uninstalled"), manifest.Name)})
	})
}

func (app *application) applyNextPluginPreset() {
	item, ok := app.selectedPlugin()
	if !ok || item.SchemaPath == "" || item.ConfigPath == "" {
		app.pluginStatus = app.texts.Text("plugin.status.noPreset")
		return
	}
	schema, err := plugins.LoadConfigSchema(item.SchemaPath)
	if err != nil || len(schema.Presets) == 0 {
		app.pluginStatus = app.texts.Text("plugin.status.presetUnavailable")
		if err != nil {
			app.pluginStatus += "：" + err.Error()
		}
		return
	}
	values, recovered, err := plugins.ReadConfigRecovering(item.ConfigPath, schema)
	if err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.configReadFailed"), err)
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
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.presetFailed"), err)
		return
	}
	app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.presetApplied"), preset.Name)
	if recovered != "" {
		app.pluginStatus += app.texts.Text("plugin.status.configRecovered")
	}
}

func (app *application) movePlugin(delta int) {
	item, ok := app.selectedPlugin()
	if !ok {
		app.pluginStatus = app.texts.Text("plugin.status.selectFirst")
		return
	}
	next := plugins.CloneState(app.pluginState)
	if err := plugins.Move(&next, item.Manifest.ID, delta); err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.moveFailed"), err)
		return
	}
	if err := plugins.SaveState(app.pluginLayout.State, next); err != nil {
		app.pluginStatus = fmt.Sprintf(app.texts.Text("plugin.status.orderSaveFailed"), err)
		return
	}
	app.pluginState = next
	app.refreshPlugins()
}

func (app *application) paintPlugins(dc win32.HDC, client win32.Rect, left int32) {
	if app.pluginTargetMode {
		app.paintFufuConfig(dc, client, left)
		return
	}
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
		rect := win32.Rect{Left: left, Top: app.pluginContentY(top), Right: right, Bottom: app.pluginContentY(bottom)}
		app.paintButtonSurface(dc, rect, brush)
		return rect
	}
	staticRow := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: app.pluginContentY(top), Right: right, Bottom: app.pluginContentY(bottom)}
		app.paintStaticSurface(dc, rect, brush)
		return rect
	}
	cell := func(rect win32.Rect, index, count int32) win32.Rect {
		return splitButtonRect(rect, index, count, app.dpi)
	}
	draw := func(value string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, value, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	safe := app.texts.Text("plugin.safe.off")
	if app.settings.Plugins.SafeMode {
		safe = app.texts.Text("plugin.safe.on")
	}
	safeButton, targetCell := pluginHeaderRects(left, right, app.pluginContentY(170), app.pluginContentY(214), app.dpi)
	app.paintButtonSurface(dc, safeButton, warningBrush)
	app.paintButtonSurface(dc, targetCell, accentBrush)
	draw(fmt.Sprintf(app.texts.Text("plugin.safe"), safe), safeButton, win32.Color(255, 205, 150))
	draw(app.texts.Text("fufu.target.openConfig"), targetCell, win32.Color(235, 238, 248))
	toolsRow := win32.Rect{Left: left, Top: app.pluginContentY(220), Right: right, Bottom: app.pluginContentY(264)}
	rescanButton, installButton := cell(toolsRow, 0, 2), cell(toolsRow, 1, 2)
	app.paintButtonSurface(dc, rescanButton, buttonBrush)
	app.paintButtonSurface(dc, installButton, buttonBrush)
	draw(app.texts.Text("plugin.rescan"), rescanButton, win32.Color(225, 229, 242))
	installText := app.texts.Text("plugin.installLocal")
	if app.pluginBusy {
		installText = app.texts.Text("plugin.busy")
	}
	draw(installText, installButton, win32.Color(190, 197, 216))
	enabled := false
	if item, ok := app.selectedPlugin(); ok {
		enabled = item.Enabled
	}
	if len(app.pluginItems) == 0 {
		draw(app.texts.Text("plugin.noneInstalled"), staticRow(270, 412, cardBrush), win32.Color(145, 154, 180))
	} else {
		for visible := 0; visible < pluginVisibleRows; visible++ {
			index := app.pluginListScroll + visible
			if index >= len(app.pluginItems) {
				break
			}
			item := app.pluginItems[index]
			brush := cardBrush
			if item.Manifest.ID == app.pluginSelected {
				brush = accentBrush
			} else if visible%2 == 1 {
				brush = buttonBrush
			}
			value := fmt.Sprintf("%s  |  %s  |  %s  |  %s", item.DisplayName(), item.Manifest.Version, item.Manifest.Developer, onOffText(app.texts.Language(), item.Enabled))
			itemRow := row(int32(270+visible*50), int32(312+visible*50), brush)
			itemRow.Right -= win32.Scale(20, app.dpi)
			draw(value, itemRow, win32.Color(225, 229, 242))
		}
	}
	draw(fmt.Sprintf(app.texts.Text("plugin.enabled"), onOffText(app.texts.Language(), enabled)), row(420, 464, accentBrush), win32.Color(235, 238, 248))
	draw(app.texts.Text("plugin.aliasHint"), staticRow(470, 514, cardBrush), win32.Color(145, 154, 180))
	actions := win32.Rect{Left: left, Top: app.pluginContentY(520), Right: right, Bottom: app.pluginContentY(564)}
	for index, action := range []struct {
		text  string
		color uint32
	}{{app.texts.Text("plugin.aliasSave"), win32.Color(225, 229, 242)}, {app.texts.Text("plugin.moveUp"), win32.Color(190, 197, 216)}, {app.texts.Text("plugin.moveDown"), win32.Color(190, 197, 216)}} {
		button := cell(actions, int32(index), 3)
		app.paintButtonSurface(dc, button, buttonBrush)
		draw(action.text, button, action.color)
	}
	lifecycle := win32.Rect{Left: left, Top: app.pluginContentY(570), Right: right, Bottom: app.pluginContentY(614)}
	for index, action := range []struct {
		text  string
		color uint32
	}{{app.texts.Text("plugin.rollback"), win32.Color(255, 205, 150)}, {app.texts.Text("plugin.uninstall"), win32.Color(255, 170, 150)}, {app.texts.Text("plugin.nextPreset"), win32.Color(255, 205, 150)}} {
		button := cell(lifecycle, int32(index), 3)
		app.paintButtonSurface(dc, button, warningBrush)
		draw(action.text, button, action.color)
	}
}

func (app *application) paintFufuConfig(dc win32.HDC, client win32.Rect, left int32) {
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
		rect := win32.Rect{Left: left, Top: app.pluginContentY(top), Right: right, Bottom: app.pluginContentY(bottom)}
		app.paintButtonSurface(dc, rect, brush)
		return rect
	}
	draw := func(value string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, value, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	targetName := app.texts.Text("fufu.target.notInstalled")
	if app.fufuTargetInstalled {
		targetName = fmt.Sprintf("%s  |  %s  |  %s", app.fufuTarget.Name, app.fufuTarget.Version, app.fufuTarget.Developer)
	}
	selectorRect, repairRect, toggleRect := fufuHeaderRects(left, right, app.pluginContentY(170), app.pluginContentY(224), app.dpi)
	app.paintStaticSurface(dc, selectorRect, cardBrush)
	app.paintButtonSurface(dc, repairRect, accentBrush)
	app.paintButtonSurface(dc, toggleRect, warningBrush)
	draw(fmt.Sprintf(app.texts.Text("fufu.target.selector"), targetName), selectorRect, win32.Color(235, 238, 248))
	draw(app.texts.Text("fufu.target.repair"), repairRect, win32.Color(225, 229, 242))
	draw(fmt.Sprintf(app.texts.Text("fufu.target.injection"), onOffText(app.texts.Language(), app.fufuTargetEnabled && app.settings.Injection.Enabled)), toggleRect, win32.Color(255, 205, 150))
	for visible := 0; visible < fufuVisibleRows; visible++ {
		index := app.fufuScroll + visible
		if index >= len(app.fufuTarget.Settings) {
			break
		}
		setting := app.fufuTarget.Settings[index]
		brush := cardBrush
		if visible%2 == 1 {
			brush = buttonBrush
		}
		settingRow := win32.Rect{Left: left, Top: app.pluginContentY(int32(235 + visible*50)), Right: right, Bottom: app.pluginContentY(int32(277 + visible*50))}
		if setting.Field.Type == "bool" {
			app.paintButtonSurface(dc, settingRow, brush)
		} else {
			app.paintStaticSurface(dc, settingRow, brush)
		}
		settingRow.Right -= win32.Scale(20, app.dpi)
		labelRect := settingRow
		labelRect.Right -= win32.Scale(280, app.dpi)
		draw(fmt.Sprintf("%s  (%s)", setting.Field.Name, setting.Field.Type), labelRect, win32.Color(225, 229, 242))
		if setting.Field.Type == "bool" {
			value := strings.ToLower(strings.TrimSpace(app.fufuValues[setting.Field.ID]))
			on := value == "1" || value == "true"
			valueRect := settingRow
			valueRect.Left = valueRect.Right - win32.Scale(250, app.dpi)
			draw(onOffText(app.texts.Language(), on), valueRect, win32.Color(235, 238, 248))
		}
	}
	footer := row(540, 584, warningBrush)
	footerText := fmt.Sprintf(app.texts.Text("fufu.setting.scroll"), min(len(app.fufuTarget.Settings), app.fufuScroll+1), min(len(app.fufuTarget.Settings), app.fufuScroll+fufuVisibleRows), len(app.fufuTarget.Settings))
	draw(app.texts.Text("fufu.target.openGeneric")+"  |  "+footerText, footer, win32.Color(190, 197, 216))
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
		rect := win32.Rect{Left: left, Top: app.pluginContentY(top), Right: right, Bottom: app.pluginContentY(bottom)}
		app.paintButtonSurface(dc, rect, brush)
		return rect
	}
	staticRow := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: app.pluginContentY(top), Right: right, Bottom: app.pluginContentY(bottom)}
		app.paintStaticSurface(dc, rect, brush)
		return rect
	}
	cell := func(rect win32.Rect, index, count int32) win32.Rect {
		return splitButtonRect(rect, index, count, app.dpi)
	}
	draw := func(value string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, value, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	draw("", staticRow(170, 214, cardBrush), win32.Color(225, 229, 242))
	draw("", staticRow(220, 264, cardBrush), win32.Color(225, 229, 242))
	syncText := app.texts.Text("plugin.store.sync")
	if app.pluginBusy {
		syncText = app.texts.Text("plugin.store.busy")
	}
	toolsRow := win32.Rect{Left: left, Top: app.pluginContentY(270), Right: right, Bottom: app.pluginContentY(314)}
	toolLabels := []string{syncText, fmt.Sprintf(app.texts.Text("plugin.store.category"), pluginCategoryText(app.texts, app.settings.Plugins.Category)), fmt.Sprintf(app.texts.Text("plugin.store.sort"), pluginSortText(app.texts, app.settings.Plugins.Sort))}
	for index, label := range toolLabels {
		button := cell(toolsRow, int32(index), 3)
		app.paintButtonSurface(dc, button, accentBrush)
		draw(label, button, win32.Color(225, 229, 242))
	}
	installAction := app.texts.Text("plugin.store.install")
	if item, ok := app.selectedCatalogPlugin(); ok {
		if installed, exists := app.pluginState.Installed[strings.ToLower(item.ID)]; exists {
			if installed.ActiveVersion == item.Version {
				installAction = app.texts.Text("plugin.store.repair")
			} else {
				installAction = app.texts.Text("plugin.store.update")
			}
		}
	}
	if len(app.pluginCatalogPage.Items) == 0 {
		draw(app.texts.Text("plugin.store.none"), staticRow(320, 462, cardBrush), win32.Color(145, 154, 180))
	} else {
		for visible := 0; visible < storeVisibleRows; visible++ {
			index := app.storeListScroll + visible
			if index >= len(app.pluginCatalogPage.Items) {
				break
			}
			item := app.pluginCatalogPage.Items[index]
			brush := cardBrush
			if item.ID == app.storeSelected {
				brush = accentBrush
			} else if visible%2 == 1 {
				brush = buttonBrush
			}
			state := ""
			if installed, exists := app.pluginState.Installed[strings.ToLower(item.ID)]; exists {
				state = "  |  " + installed.ActiveVersion
			}
			value := fmt.Sprintf("%s  |  %s  |  %s%s", item.Name, item.Version, item.Developer, state)
			itemRow := row(int32(320+visible*50), int32(362+visible*50), brush)
			itemRow.Right -= win32.Scale(20, app.dpi)
			draw(value, itemRow, win32.Color(225, 229, 242))
		}
	}
	pages := win32.Rect{Left: left, Top: app.pluginContentY(470), Right: right, Bottom: app.pluginContentY(514)}
	previousButton, nextButton := cell(pages, 0, 2), cell(pages, 1, 2)
	app.paintButtonSurface(dc, previousButton, buttonBrush)
	app.paintButtonSurface(dc, nextButton, buttonBrush)
	draw(app.texts.Text("plugin.store.previous"), previousButton, win32.Color(190, 197, 216))
	draw(fmt.Sprintf(app.texts.Text("plugin.store.next"), app.pluginCatalogPage.Page, app.pluginCatalogPage.TotalPages), nextButton, win32.Color(190, 197, 216))
	draw(installAction, row(520, 564, accentBrush), win32.Color(235, 238, 248))
	draw(app.pluginStatus, staticRow(570, 614, cardBrush), win32.Color(145, 154, 180))
}

func pluginCategoryText(texts localization.Catalog, category string) string {
	if category == "" {
		category = "all"
	}
	return texts.Text("plugin.store.category." + category)
}

func pluginSortText(texts localization.Catalog, sortMode string) string {
	return texts.Text("plugin.store.sort." + sortMode)
}

func (app *application) saveShellSettings(next shellconfig.Config) bool {
	normalized, err := next.Normalized()
	if err != nil {
		app.shellStatus = err.Error()
		return false
	}
	settings := app.settings
	settings.Shell = normalized
	if err := config.Save(app.layout.Config, settings); err != nil {
		app.shellStatus = app.texts.Text("settings.status.saveFailed") + err.Error()
		return false
	}
	app.settings = settings
	app.shellStatus = app.texts.Text("settings.status.saved")
	return true
}

func (app *application) applyProcessPriority() error {
	priority := uint32(win32.NORMAL_PRIORITY_CLASS)
	switch app.settings.Shell.ProcessPriority {
	case shellconfig.PriorityBelowNormal:
		priority = win32.BELOW_NORMAL_PRIORITY_CLASS
	case shellconfig.PriorityAboveNormal:
		priority = win32.ABOVE_NORMAL_PRIORITY_CLASS
	}
	return win32.SetCurrentProcessPriority(priority)
}

func (app *application) publishCPUWarningConfig() {
	app.cpuWarning.Store(&cpuWarningConfig{
		Enabled:    app.settings.Shell.CPUWarningEnabled,
		Threshold:  float64(app.settings.Shell.CPUWarningThreshold),
		DurationMS: app.settings.Shell.CPUWarningDurationMS,
	})
}

func (app *application) settingsClick(y int) {
	sy := func(value int32) int { return int(app.pluginContentY(value)) }
	switch {
	case y >= sy(170) && y < sy(214):
		languages := []string{shellconfig.LanguageSystem, shellconfig.LanguageZH, shellconfig.LanguageEN}
		index := 0
		for i, value := range languages {
			if app.settings.Shell.Language == value {
				index = (i + 1) % len(languages)
				break
			}
		}
		next := app.settings.Shell
		next.Language = languages[index]
		if app.saveShellSettings(next) {
			app.texts = localization.New(localization.Language(app.settings.Shell.Language), win32.UserDefaultLocaleName())
			win32.SetWindowText(app.hwnd, app.texts.Text("app.title")+" "+app.build.Version)
			app.refreshLocalizedCueBanners()
			app.addTrayIcon()
		}
	case y >= sy(220) && y < sy(264):
		next := app.settings.Shell
		if next.Theme == shellconfig.ThemeDark {
			next.Theme = shellconfig.ThemeSystem
		} else {
			next.Theme = shellconfig.ThemeDark
		}
		if app.saveShellSettings(next) {
			app.refreshTheme()
		}
	case y >= sy(270) && y < sy(314):
		next := app.settings.Shell
		next.MinimizeToTray = !next.MinimizeToTray
		app.saveShellSettings(next)
	case y >= sy(320) && y < sy(364):
		next := app.settings.Shell
		next.RememberWindowSize = !next.RememberWindowSize
		app.saveShellSettings(next)
	case y >= sy(370) && y < sy(414):
		next := app.settings.Shell
		next.EnforceMinimumSize = !next.EnforceMinimumSize
		app.saveShellSettings(next)
	case y >= sy(420) && y < sy(464):
		priorities := []string{shellconfig.PriorityBelowNormal, shellconfig.PriorityNormal, shellconfig.PriorityAboveNormal}
		index := 0
		for i, value := range priorities {
			if app.settings.Shell.ProcessPriority == value {
				index = (i + 1) % len(priorities)
				break
			}
		}
		next := app.settings.Shell
		next.ProcessPriority = priorities[index]
		previous := app.settings.Shell.ProcessPriority
		app.settings.Shell.ProcessPriority = next.ProcessPriority
		if err := app.applyProcessPriority(); err != nil {
			app.settings.Shell.ProcessPriority = previous
			app.shellStatus = fmt.Sprintf(app.texts.Text("settings.status.priorityFailed"), err)
		} else if !app.saveShellSettings(next) {
			app.settings.Shell.ProcessPriority = previous
			_ = app.applyProcessPriority()
		}
	case y >= sy(470) && y < sy(514):
		next := app.settings.Shell
		next.CPUWarningEnabled = !next.CPUWarningEnabled
		app.saveShellSettings(next)
	case y >= sy(520) && y < sy(564):
		app.startDiagnosticExport()
	case y >= sy(670) && y < sy(714):
		if app.updateRelease != nil {
			app.startUpdateApply()
		} else {
			app.startUpdateCheck()
		}
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) updateStatus(english, chinese string) string {
	if app.settings.Shell.Language == shellconfig.LanguageEN {
		return english
	}
	return chinese
}

func (app *application) startUpdateCheck() {
	if app.updateBusy {
		return
	}
	app.updateBusy = true
	app.updateRelease = nil
	app.shellStatus = app.updateStatus("checking for updates...", "正在检查更新……")
	app.updateTask = app.tasks.Run(func(ctx context.Context, taskID uint64) {
		coordinator := selfupdate.Coordinator{InstallRoot: app.layout.Root, CurrentVersion: app.build.Version}
		release, err := coordinator.Check(ctx)
		result := updateCheckUpdate{taskID: taskID, err: err}
		if err == nil {
			result.release = &release
		}
		select {
		case app.updateUpdates <- result:
		default:
		}
		win32.PostMessage(app.hwnd, messageUpdate, 0, 0)
	})
}

func (app *application) startUpdateApply() {
	if app.updateBusy || app.updateRelease == nil {
		return
	}
	release := *app.updateRelease
	app.updateBusy = true
	app.shellStatus = app.updateStatus("downloading and verifying update...", "正在下载并校验更新……")
	app.updateTask = app.tasks.Run(func(ctx context.Context, taskID uint64) {
		coordinator := selfupdate.Coordinator{InstallRoot: app.layout.Root, CurrentVersion: app.build.Version}
		staged, err := coordinator.DownloadAndStage(ctx, release)
		if err == nil {
			_, err = coordinator.PrepareAndLaunch(ctx, staged)
		}
		result := updateCheckUpdate{taskID: taskID, apply: true, err: err}
		select {
		case app.updateUpdates <- result:
		default:
		}
		win32.PostMessage(app.hwnd, messageUpdate, 0, 0)
	})
}

func (app *application) paintSettings(dc win32.HDC, client win32.Rect, left int32) {
	cardBrush := win32.CreateSolidBrush(win32.Color(25, 29, 39))
	defer win32.DeleteObject(uintptr(cardBrush))
	buttonBrush := win32.CreateSolidBrush(win32.Color(35, 40, 54))
	defer win32.DeleteObject(uintptr(buttonBrush))
	accentBrush := win32.CreateSolidBrush(win32.Color(52, 66, 112))
	defer win32.DeleteObject(uintptr(accentBrush))
	right := client.Right - win32.Scale(42, app.dpi)
	row := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: app.pluginContentY(top), Right: right, Bottom: app.pluginContentY(bottom)}
		app.paintButtonSurface(dc, rect, brush)
		return rect
	}
	staticRow := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: app.pluginContentY(top), Right: right, Bottom: app.pluginContentY(bottom)}
		app.paintStaticSurface(dc, rect, brush)
		return rect
	}
	draw := func(value string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, value, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	languageKey := map[string]string{shellconfig.LanguageSystem: "settings.language.system", shellconfig.LanguageZH: "settings.language.zh", shellconfig.LanguageEN: "settings.language.en"}[app.settings.Shell.Language]
	draw(fmt.Sprintf(app.texts.Text("common.label"), app.texts.Text("settings.language"), app.texts.Text(languageKey)), row(170, 214, accentBrush), win32.Color(235, 238, 248))
	draw(fmt.Sprintf(app.texts.Text("common.label"), app.texts.Text("settings.theme"), app.texts.Text("settings.theme."+app.settings.Shell.Theme)), row(220, 264, buttonBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf(app.texts.Text("common.label"), app.texts.Text("settings.tray"), onOffText(app.texts.Language(), app.settings.Shell.MinimizeToTray)), row(270, 314, buttonBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf(app.texts.Text("common.label"), app.texts.Text("settings.rememberWindow"), onOffText(app.texts.Language(), app.settings.Shell.RememberWindowSize)), row(320, 364, buttonBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf(app.texts.Text("common.label"), app.texts.Text("settings.minimumSize"), onOffText(app.texts.Language(), app.settings.Shell.EnforceMinimumSize)), row(370, 414, buttonBrush), win32.Color(225, 229, 242))
	priorityKey := map[string]string{shellconfig.PriorityBelowNormal: "settings.priority.below", shellconfig.PriorityNormal: "settings.priority.normal", shellconfig.PriorityAboveNormal: "settings.priority.above"}[app.settings.Shell.ProcessPriority]
	draw(fmt.Sprintf(app.texts.Text("common.label"), app.texts.Text("settings.priority"), app.texts.Text(priorityKey)), row(420, 464, buttonBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf(app.texts.Text("settings.cpuWarning.summary"), app.texts.Text("settings.cpuWarning"), onOffText(app.texts.Language(), app.settings.Shell.CPUWarningEnabled), app.settings.Shell.CPUWarningThreshold, float64(app.settings.Shell.CPUWarningDurationMS)/1000), row(470, 514, buttonBrush), win32.Color(225, 229, 242))
	diagnosticText := app.texts.Text("settings.diagnostics")
	if app.diagnosticBusy {
		diagnosticText = app.texts.Text("settings.status.exporting")
	}
	draw(diagnosticText, row(520, 564, buttonBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf("%s · v%s · %s · %s b5a050ebd319", app.texts.Text("settings.about"), app.build.Version, app.build.Commit, app.texts.Text("settings.upstream")), staticRow(570, 614, cardBrush), win32.Color(166, 174, 197))
	draw(app.shellStatus, staticRow(620, 660, cardBrush), win32.Color(145, 154, 180))
	updateText := app.updateStatus("Check for updates", "检查更新")
	if app.updateBusy {
		updateText = app.updateStatus("Checking for updates...", "正在检查更新……")
	} else if app.updateRelease != nil {
		updateText = app.updateStatus("Update available; click to check again", "发现更新；单击重新检查")
	}
	draw(updateText, row(670, 714, buttonBrush), win32.Color(225, 229, 242))
}

func onOffText(language localization.Language, enabled bool) string {
	texts := localization.New(language, "")
	if enabled {
		return texts.Text("common.on")
	}
	return texts.Text("common.off")
}

func (app *application) refreshTheme() {
	app.palette = uitheme.Current(app.settings.Shell.Theme)
	win32.SetColorTransform(app.palette.Map)
	if app.editBrush != 0 {
		win32.DeleteObject(uintptr(app.editBrush))
	}
	app.editBrush = win32.CreateSolidBrush(win32.Color(25, 29, 39))
	for _, control := range []win32.HWND{app.customArgumentsEdit, app.pluginAliasEdit, app.pluginTokenEdit, app.pluginSearchEdit, app.listScrollbar} {
		if control != 0 {
			win32.SetControlDarkTheme(control, app.palette.Dark)
			win32.Invalidate(control)
		}
	}
	for _, control := range app.fufuEdits {
		win32.SetControlDarkTheme(control, app.palette.Dark)
		win32.Invalidate(control)
	}
	if app.hwnd != 0 {
		win32.SetDarkTitleBar(app.hwnd, app.palette.Dark)
		win32.Invalidate(app.hwnd)
	}
}

func (app *application) startDiagnosticExport() {
	if app.diagnosticBusy {
		return
	}
	directory, selected, err := win32.SelectFolderWithTitle(app.hwnd, app.layout.Root, app.texts.Text("settings.diagnostics.prompt"))
	if err != nil {
		app.shellStatus = app.texts.Text("settings.status.exportFailed") + err.Error()
		return
	}
	if !selected {
		return
	}
	destination := filepath.Join(directory, "genshin-tools-diagnostics-"+time.Now().Format("20060102-150405")+".json")
	resources := app.lastSnap.Resources
	input := diagnostics.ExportInput{
		Build:          app.build,
		UpstreamCommit: "b5a050ebd319341bddc4189491c90c22162d33fa",
		MonitorCount:   win32.MonitorCount(),
		DPI:            app.dpi,
		Resources: diagnostics.ResourceReport{
			CPUPercent: app.lastSnap.CPUPercent,
			Goroutines: runtime.NumGoroutine(),
			Threads:    resources.Threads,
			Handles:    resources.Handles,
			USER:       resources.USER,
			GDI:        resources.GDI,
		},
		Config: diagnostics.ConfigSummary{
			SchemaVersion: app.settings.SchemaVersion,
			Language:      app.settings.Shell.Language,
			Theme:         app.settings.Shell.Theme,
			Tray:          app.settings.Shell.MinimizeToTray,
			RememberSize:  app.settings.Shell.RememberWindowSize,
			MinimumSize:   app.settings.Shell.EnforceMinimumSize,
			Priority:      app.settings.Shell.ProcessPriority,
			CPUWarning:    app.settings.Shell.CPUWarningEnabled,
			InputMode:     app.settings.Input.Mode.String(),
			InputInterval: app.settings.Input.IntervalMS,
			PluginCount:   len(app.pluginItems),
			PluginSafe:    app.settings.Plugins.SafeMode,
			Features: map[string]bool{
				"capture":   app.settings.Capture.Enabled,
				"overlay":   app.settings.Overlay.Enabled,
				"injection": app.settings.Injection.Enabled,
				"betterGI":  app.settings.LocalEnhance.BetterGIEnabled,
			},
		},
		LogPath: filepath.Join(app.layout.Logs, "genshin-tools.log"),
	}
	app.diagnosticBusy = true
	app.shellStatus = app.texts.Text("settings.status.exporting")
	app.tasks.Run(func(ctx context.Context, _ uint64) {
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		input.Resources.HeapBytes = memory.HeapAlloc
		exportErr := diagnostics.Export(destination, input)
		select {
		case <-ctx.Done():
			return
		case app.diagnosticUpdates <- diagnosticUpdate{path: destination, err: exportErr}:
			win32.PostMessage(app.hwnd, messageDiagnostics, 0, 0)
		}
	})
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
		mode := -1
		modeWidth := win32.Scale(132, app.dpi)
		for index := 0; index <= int(input.ModeMouseRight); index++ {
			rect := win32.Rect{Left: int32(left) + int32(index)*modeWidth, Top: win32.Scale(226, app.dpi), Right: int32(left) + int32(index+1)*modeWidth - win32.Scale(8, app.dpi), Bottom: win32.Scale(266, app.dpi)}
			if pointInButton(rect, int32(x), int32(y)) {
				mode = index
				break
			}
		}
		if mode >= 0 {
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
		app.inputUIError = app.texts.Text("input.error.captureConflict")
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
		app.paintButtonSurface(dc, rect, brush)
		return rect
	}
	staticRow := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		app.paintStaticSurface(dc, rect, brush)
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
	action := app.texts.Text("common.enable")
	if config.Enabled {
		action = app.texts.Text("common.disable")
	}
	draw(fmt.Sprintf(app.texts.Text("input.toggle"), inputStateText(app.texts, snapshot.State), action), row(170, 216, toggleBrush), win32.Color(235, 238, 248))

	modeWidth := win32.Scale(132, app.dpi)
	modeNames := []string{app.texts.Text("input.mode.keyboard"), app.texts.Text("input.mode.mouseLeft"), app.texts.Text("input.mode.mouseRight")}
	for index, name := range modeNames {
		rect := win32.Rect{Left: left + int32(index)*modeWidth, Top: win32.Scale(226, app.dpi), Right: left + int32(index+1)*modeWidth - win32.Scale(8, app.dpi), Bottom: win32.Scale(266, app.dpi)}
		brush := buttonBrush
		if int(config.Mode) == index {
			brush = activeBrush
		}
		app.paintButtonSurface(dc, rect, brush)
		draw(name, rect, win32.Color(225, 229, 242))
	}
	if config.Mode == input.ModeKeyboard {
		draw(recordLabel(app.texts, "input.key.trigger", config.TriggerKey, app.recording == 1), row(276, 316, buttonBrush), win32.Color(225, 229, 242))
		draw(recordLabel(app.texts, "input.key.output", config.OutputKey, app.recording == 2), row(326, 366, buttonBrush), win32.Color(225, 229, 242))
	} else {
		draw(app.texts.Text("input.mouseTrigger"), staticRow(276, 366, cardBrush), win32.Color(166, 174, 197))
	}
	draw(recordLabel(app.texts, "input.key.stop", config.StopKey, app.recording == 3), row(376, 416, buttonBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf(app.texts.Text("input.interval"), config.IntervalMS), row(426, 466, buttonBrush), win32.Color(225, 229, 242))
	visibleError := snapshot.LastError
	if visibleError == "" {
		visibleError = app.inputUIError
	}
	if visibleError != "" {
		draw(fmt.Sprintf(app.texts.Text("input.error"), visibleError), staticRow(476, 516, cardBrush), win32.Color(255, 126, 126))
	} else {
		draw(fmt.Sprintf(app.texts.Text("input.outputCount"), snapshot.OutputCount), staticRow(476, 516, cardBrush), win32.Color(145, 154, 180))
	}
}

func inputStateText(texts localization.Catalog, state input.State) string {
	switch state {
	case input.StateDisabled:
		return texts.Text("input.state.disabled")
	case input.StateArmed:
		return texts.Text("input.state.armed")
	case input.StateRunning:
		return texts.Text("input.state.running")
	case input.StateStopping:
		return texts.Text("input.state.stopping")
	case input.StateFault:
		return texts.Text("input.state.fault")
	default:
		return texts.Text("input.state.unknown")
	}
}

func recordLabel(texts localization.Catalog, nameKey string, key uint32, recording bool) string {
	name := texts.Text(nameKey)
	if recording {
		return fmt.Sprintf(texts.Text("input.record.pending"), name)
	}
	return fmt.Sprintf(texts.Text("input.record.ready"), name, virtualKeyName(key))
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
	win32.SetControlFont(edit, app.fontBody)
	win32.SetControlDarkTheme(edit, app.palette.Dark)
	aliasEdit, err := win32.CreateControl("EDIT", "", win32.WS_CHILD|win32.WS_BORDER|win32.WS_TABSTOP|win32.ES_AUTOHSCROLL, 0, 0, 100, 32, app.hwnd, 2002, app.instance)
	if err != nil {
		return err
	}
	app.pluginAliasEdit = aliasEdit
	win32.SetTextLimit(aliasEdit, 64)
	win32.SetControlFont(aliasEdit, app.fontBody)
	win32.SetControlDarkTheme(aliasEdit, app.palette.Dark)
	tokenEdit, err := win32.CreateControl("EDIT", "", win32.WS_CHILD|win32.WS_BORDER|win32.WS_TABSTOP|win32.ES_AUTOHSCROLL|win32.ES_PASSWORD, 0, 0, 100, 32, app.hwnd, 2003, app.instance)
	if err != nil {
		return err
	}
	app.pluginTokenEdit = tokenEdit
	win32.SetTextLimit(tokenEdit, 4096)
	win32.SetControlFont(tokenEdit, app.fontBody)
	win32.SetControlDarkTheme(tokenEdit, app.palette.Dark)
	searchEdit, err := win32.CreateControl("EDIT", app.settings.Plugins.Search, win32.WS_CHILD|win32.WS_BORDER|win32.WS_TABSTOP|win32.ES_AUTOHSCROLL, 0, 0, 100, 32, app.hwnd, 2004, app.instance)
	if err != nil {
		return err
	}
	app.pluginSearchEdit = searchEdit
	win32.SetTextLimit(searchEdit, 128)
	win32.SetControlFont(searchEdit, app.fontBody)
	win32.SetControlDarkTheme(searchEdit, app.palette.Dark)
	scrollbar, err := win32.CreateControl("SCROLLBAR", "", win32.WS_CHILD|win32.SBS_VERT, 0, 0, 16, 100, app.hwnd, 2005, app.instance)
	if err != nil {
		return err
	}
	app.listScrollbar = scrollbar
	win32.SetControlDarkTheme(scrollbar, app.palette.Dark)
	app.rebuildFufuControls()
	app.refreshLocalizedCueBanners()
	app.layoutLaunchControls()
	app.updateLaunchControlVisibility()
	return nil
}

func (app *application) refreshLocalizedCueBanners() {
	if app.customArgumentsEdit != 0 {
		win32.SetCueBanner(app.customArgumentsEdit, app.texts.Text("game.customArgumentsCue"))
	}
	if app.pluginAliasEdit != 0 {
		win32.SetCueBanner(app.pluginAliasEdit, app.texts.Text("plugin.aliasCue"))
	}
	if app.pluginTokenEdit != 0 {
		win32.SetCueBanner(app.pluginTokenEdit, app.texts.Text("plugin.store.tokenCue"))
	}
	if app.pluginSearchEdit != 0 {
		win32.SetCueBanner(app.pluginSearchEdit, app.texts.Text("plugin.store.searchCue"))
	}
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
		aliasTop, aliasBottom := app.pluginContentY(470), app.pluginContentY(506)
		win32.SetWindowPos(app.pluginAliasEdit, win32.Rect{Left: left, Top: aliasTop, Right: right, Bottom: aliasBottom}, win32.SWP_NOZORDER)
	}
	if app.pluginTokenEdit != 0 {
		tokenTop, tokenBottom := app.pluginContentY(170), app.pluginContentY(206)
		win32.SetWindowPos(app.pluginTokenEdit, win32.Rect{Left: left, Top: tokenTop, Right: right, Bottom: tokenBottom}, win32.SWP_NOZORDER)
	}
	if app.pluginSearchEdit != 0 {
		searchTop, searchBottom := app.pluginContentY(220), app.pluginContentY(256)
		win32.SetWindowPos(app.pluginSearchEdit, win32.Rect{Left: left, Top: searchTop, Right: right, Bottom: searchBottom}, win32.SWP_NOZORDER)
	}
	app.layoutFufuControls()
	app.syncListScrollbar()
}

func (app *application) layoutFufuControls() {
	if app.hwnd == 0 {
		return
	}
	client := win32.GetClientRect(app.hwnd)
	right := client.Right - win32.Scale(60, app.dpi)
	left := max(win32.Scale(500, app.dpi), right-win32.Scale(250, app.dpi))
	visiblePage := app.selected == 8 && app.pluginTargetMode && app.fufuTargetInstalled
	for index, setting := range app.fufuTarget.Settings {
		control := app.fufuEdits[setting.Field.ID]
		if control == 0 {
			continue
		}
		visible := index - app.fufuScroll
		if visiblePage && visible >= 0 && visible < fufuVisibleRows {
			top := app.pluginContentY(int32(241 + visible*50))
			bottom := app.pluginContentY(int32(273 + visible*50))
			win32.SetWindowPos(control, win32.Rect{Left: left, Top: top, Right: right, Bottom: bottom}, win32.SWP_NOZORDER)
			win32.ShowWindow(control, win32.SW_SHOWNORMAL)
		} else {
			win32.ShowWindow(control, win32.SW_HIDE)
		}
	}
}

func (app *application) updateLaunchControlVisibility() {
	if app.customArgumentsEdit != 0 && app.selected == 1 {
		win32.ShowWindow(app.customArgumentsEdit, win32.SW_SHOWNORMAL)
	} else if app.customArgumentsEdit != 0 {
		win32.ShowWindow(app.customArgumentsEdit, win32.SW_HIDE)
	}
	if app.pluginAliasEdit != 0 && app.selected == 8 && !app.pluginTargetMode {
		app.syncPluginAliasEdit()
		win32.ShowWindow(app.pluginAliasEdit, win32.SW_SHOWNORMAL)
	} else if app.pluginAliasEdit != 0 {
		win32.ShowWindow(app.pluginAliasEdit, win32.SW_HIDE)
	}
	if app.pluginTokenEdit != 0 && app.selected == 9 {
		win32.ShowWindow(app.pluginTokenEdit, win32.SW_SHOWNORMAL)
	} else if app.pluginTokenEdit != 0 {
		win32.ShowWindow(app.pluginTokenEdit, win32.SW_HIDE)
	}
	if app.pluginSearchEdit != 0 && app.selected == 9 {
		win32.ShowWindow(app.pluginSearchEdit, win32.SW_SHOWNORMAL)
	} else if app.pluginSearchEdit != 0 {
		win32.ShowWindow(app.pluginSearchEdit, win32.SW_HIDE)
	}
	app.layoutFufuControls()
	app.syncListScrollbar()
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
	return app.commitLaunchConfig(app.settings.Launch)
}

func (app *application) commitLaunchConfig(next launch.Config) bool {
	if app.customArgumentsEdit != 0 {
		next.CustomArguments = win32.GetWindowText(app.customArgumentsEdit)
	}
	normalized, err := next.Normalized()
	if err != nil {
		app.launchUIError = fmt.Sprintf(app.texts.Text("game.status.launchInvalid"), err)
		return false
	}
	settings := app.settings
	settings.Launch = normalized
	if err := config.Save(app.layout.Config, settings); err != nil {
		app.launchUIError = fmt.Sprintf(app.texts.Text("game.status.saveLaunchFailed"), err)
		return false
	}
	app.settings = settings
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
		if !state.Committing && y >= sy(270) && y < sy(314) {
			app.tasks.Cancel(app.serverTask)
			state.Status = app.texts.Text("server.status.canceling")
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
		state.Status = app.texts.Text("server.status.modeChanged")
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
		state.Status = app.texts.Text("server.status.targetChanged")
		state.Error = ""
		app.serverState = state
	case y >= sy(270) && y < sy(314):
		app.startServerPlan()
		return
	case y >= sy(320) && y < sy(364):
		if state.Transaction == nil {
			state.Error = app.texts.Text("server.error.needPreview")
		} else if len(app.gameState.Running) > 0 {
			state.Error = app.texts.Text("server.error.gameRunning")
		} else if !state.Confirm {
			state.Confirm = true
			state.Status = app.texts.Text("server.status.confirm")
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
		app.serverState.Error = app.texts.Text("server.error.selectGame")
		win32.Invalidate(app.hwnd)
		return
	}
	if !app.serverState.Advanced && app.gameState.Candidate.Server == game.ServerGlobal {
		app.serverState.Error = app.texts.Text("server.error.quickMainlandOnly")
		win32.Invalidate(app.hwnd)
		return
	}
	root := app.gameState.Candidate.Root
	state := app.serverState
	if state.Transaction != nil {
		_ = state.Transaction.Abort()
	}
	state.Busy, state.Committing, state.Confirm, state.Error = true, false, false, ""
	state.Status = app.texts.Text("server.status.readingQuick")
	if state.Advanced {
		state.Status = app.texts.Text("server.status.readingAdvanced")
	}
	state.Transaction = nil
	app.serverState = state
	texts := app.texts
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
				state.Status, state.Error = texts.Text("server.status.previewCanceled"), ""
			} else {
				state.Status, state.Error = texts.Text("server.status.previewFailed"), err.Error()
			}
		} else {
			state.Transaction = transaction
			state.Status = fmt.Sprintf(texts.Text("server.status.previewComplete"), len(state.Plan.Items))
		}
		app.publishServer(id, state, false)
	})
}

func (app *application) startServerCommit() {
	state := app.serverState
	state.Busy, state.Committing, state.Confirm, state.Error = true, true, false, ""
	state.Status = app.texts.Text("server.status.committing")
	app.serverState = state
	texts := app.texts
	app.serverTask = app.tasks.Run(func(_ context.Context, id uint64) {
		err := state.Transaction.Commit(state.Plan)
		state.Busy, state.Committing = false, false
		if err != nil {
			state.Status, state.Error = texts.Text("server.status.commitFailed"), err.Error()
		} else {
			state.Status = texts.Text("server.status.complete")
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
		app.paintButtonSurface(dc, rect, brush)
		return rect
	}
	staticRow := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		app.paintStaticSurface(dc, rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	state := app.serverState
	mode := app.texts.Text("server.mode.quick")
	if state.Advanced {
		mode = app.texts.Text("server.mode.advanced")
	}
	draw(fmt.Sprintf(app.texts.Text("server.mode"), mode), row(170, 214, accentBrush), win32.Color(235, 238, 248))
	target := quickServerText(app.texts, state.Target)
	if state.Advanced {
		target = advancedServerText(app.texts, state.AdvancedTarget)
	}
	draw(fmt.Sprintf(app.texts.Text("server.target"), target), row(220, 264, accentBrush), win32.Color(235, 238, 248))
	planText := app.texts.Text("server.plan")
	if state.Busy {
		if state.Committing {
			planText = app.texts.Text("server.commit.inProgress")
		} else {
			planText = app.texts.Text("server.cancel")
		}
	}
	draw(planText, row(270, 314, buttonBrush), win32.Color(225, 229, 242))
	commit := app.texts.Text("server.commit.needPlan")
	if state.Transaction != nil {
		commit = fmt.Sprintf(app.texts.Text("server.commit.count"), len(state.Plan.Items))
	}
	if state.Confirm {
		commit = app.texts.Text("server.commit.confirm")
	}
	draw(commit, row(320, 364, buttonBrush), win32.Color(225, 229, 242))
	status, color := state.Status, win32.Color(145, 154, 180)
	if state.Error != "" {
		status, color = state.Error, win32.Color(255, 126, 126)
	}
	draw(status, staticRow(376, 420, cardBrush), color)
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
		draw(fmt.Sprintf(app.texts.Text("server.planSummary"), installs, moves, deletes, formatBytes(uint64(state.Plan.DownloadBytes))), staticRow(426, 470, cardBrush), win32.Color(190, 197, 216))
	}
	draw(app.texts.Text("server.advancedInfo"), staticRow(482, 526, cardBrush), win32.Color(166, 174, 197))
	draw(app.texts.Text("server.rollbackInfo"), staticRow(532, 576, cardBrush), win32.Color(126, 136, 160))
}

func quickServerText(texts localization.Catalog, server localenhance.QuickServer) string {
	if server == localenhance.QuickBilibili {
		return texts.Text("server.target.bilibili")
	}
	return texts.Text("server.target.official")
}

func advancedServerText(texts localization.Catalog, server localenhance.AdvancedServer) string {
	if server == localenhance.AdvancedGlobal {
		return texts.Text("server.target.global")
	}
	return texts.Text("server.target.official")
}

func (app *application) saveLocalEnhance(next config.LocalEnhanceConfig) bool {
	settings := app.settings
	settings.LocalEnhance = next
	if err := config.Save(app.layout.Config, settings); err != nil {
		app.localStatus = fmt.Sprintf(app.texts.Text("local.status.saveFailed"), err)
		return false
	}
	app.settings = settings
	return true
}

func (app *application) localEnhanceClick(_, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	settings := app.settings.LocalEnhance
	switch {
	case y >= sy(170) && y < sy(214):
		settings.HDR.Enabled = !settings.HDR.Enabled
		if app.saveLocalEnhance(settings) {
			app.localStatus = app.texts.Text("local.status.hdrTargetChanged")
		}
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
		if app.saveLocalEnhance(settings) {
			app.localStatus = app.texts.Text("local.status.hdrPresetChanged")
		}
	case y >= sy(264) && y < sy(302):
		if app.gameState.Candidate == nil || app.gameState.Candidate.Server == game.ServerGlobal {
			app.localStatus = app.texts.Text("local.status.hdrMainlandOnly")
			break
		}
		backup := app.layout.Data + string(os.PathSeparator) + "hdr-registry-backup.json"
		if err := localenhance.ApplyHDRWithBackup(localenhance.NativeRegistry{}, settings.HDR, backup); err != nil {
			app.localStatus = fmt.Sprintf(app.texts.Text("local.status.hdrApplyFailed"), err)
		} else {
			app.localStatus = app.texts.Text("local.status.hdrApplied")
		}
	case y >= sy(308) && y < sy(346):
		backup := app.layout.Data + string(os.PathSeparator) + "hdr-registry-backup.json"
		if err := localenhance.RestoreHDRBackup(localenhance.NativeRegistry{}, backup); err != nil {
			app.localStatus = fmt.Sprintf(app.texts.Text("local.status.hdrRestoreFailed"), err)
		} else {
			if current, _, err := localenhance.ReadHDR(localenhance.NativeRegistry{}); err == nil {
				settings.HDR = current
				if !app.saveLocalEnhance(settings) {
					break
				}
			}
			app.localStatus = app.texts.Text("local.status.hdrRestored")
		}
	case y >= sy(352) && y < sy(390):
		initial := ""
		if settings.StartupSoundPath != "" {
			initial = filepath.Dir(settings.StartupSoundPath)
		}
		path, selected, err := win32.SelectWaveFileWithTitle(app.hwnd, initial, app.texts.Text("local.sound.prompt"))
		if err != nil {
			app.localStatus = fmt.Sprintf(app.texts.Text("local.status.soundSelectFailed"), err)
		} else if selected {
			if err := localenhance.PlayStartupSound(path); err != nil {
				app.localStatus = fmt.Sprintf(app.texts.Text("local.status.soundPreviewFailed"), err)
			} else {
				settings.StartupSoundPath = path
				settings.StartupSoundEnabled = true
				if app.saveLocalEnhance(settings) {
					app.localStatus = app.texts.Text("local.status.soundSelected")
				}
			}
		}
	case y >= sy(396) && y < sy(434):
		settings.StartupSoundEnabled = !settings.StartupSoundEnabled
		if app.saveLocalEnhance(settings) {
			app.localStatus = fmt.Sprintf(app.texts.Text("local.status.soundEnabled"), onOffText(app.texts.Language(), settings.StartupSoundEnabled))
		}
	case y >= sy(440) && y < sy(478):
		settings.BetterGIEnabled = !settings.BetterGIEnabled
		if app.saveLocalEnhance(settings) {
			app.localStatus = fmt.Sprintf(app.texts.Text("local.status.betterGIEnabled"), onOffText(app.texts.Language(), settings.BetterGIEnabled))
		}
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
		if app.saveLocalEnhance(settings) {
			app.localStatus = app.texts.Text("local.status.betterGIDelay")
		}
	case y >= sy(528) && y < sy(566):
		info, err := localenhance.AuditBetterGI()
		if err != nil {
			app.localStatus = fmt.Sprintf(app.texts.Text("local.status.betterGIAuditFailed"), err)
		} else if !info.Registered {
			app.localStatus = app.texts.Text("local.status.betterGINotFound")
		} else if err := localenhance.StartBetterGI(); err != nil {
			app.localStatus = fmt.Sprintf(app.texts.Text("local.status.betterGIStartFailed"), err)
		} else {
			app.localStatus = app.texts.Text("local.status.betterGIStarted")
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
		app.paintButtonSurface(dc, rect, brush)
		return rect
	}
	staticRow := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		app.paintStaticSurface(dc, rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	settings := app.settings.LocalEnhance
	draw(fmt.Sprintf(app.texts.Text("local.hdrState"), onOffText(app.texts.Language(), settings.HDR.Enabled)), row(170, 214, accentBrush), win32.Color(235, 238, 248))
	draw(fmt.Sprintf(app.texts.Text("local.brightness"), settings.HDR.MaxLuminance, settings.HDR.SceneLuminance, settings.HDR.UILuminance), row(220, 258, buttonBrush), win32.Color(225, 229, 242))
	draw(app.texts.Text("local.hdrApply"), row(264, 302, buttonBrush), win32.Color(225, 229, 242))
	draw(app.texts.Text("local.hdrRestore"), row(308, 346, buttonBrush), win32.Color(225, 229, 242))
	sound := localizedValueOrUnknown(app.texts, settings.StartupSoundPath)
	draw(fmt.Sprintf(app.texts.Text("local.sound.select"), sound), row(352, 390, buttonBrush), win32.Color(190, 197, 216))
	draw(fmt.Sprintf(app.texts.Text("local.sound.enabled"), onOffText(app.texts.Language(), settings.StartupSoundEnabled)), row(396, 434, buttonBrush), win32.Color(190, 197, 216))
	draw(fmt.Sprintf(app.texts.Text("local.betterGI.enabled"), onOffText(app.texts.Language(), settings.BetterGIEnabled)), row(440, 478, buttonBrush), win32.Color(190, 197, 216))
	draw(fmt.Sprintf(app.texts.Text("local.betterGI.delay"), float64(settings.BetterGIDelayMS)/1000), row(484, 522, buttonBrush), win32.Color(190, 197, 216))
	draw(app.texts.Text("local.betterGI.audit"), row(528, 566, buttonBrush), win32.Color(190, 197, 216))
	status := app.localStatus
	if status == "" {
		status = app.texts.Text("local.safety")
	}
	draw(status, staticRow(572, 610, cardBrush), win32.Color(145, 154, 180))
}

func (app *application) gameClick(x, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	switch {
	case y >= sy(170) && y < sy(214):
		if app.gameState.Candidate == nil || app.launchEngine == nil {
			app.launchUIError = app.texts.Text("game.status.selectFirst")
			break
		}
		if !app.syncLaunchConfig() {
			break
		}
		app.launchUIError = ""
		if err := app.launchEngine.Launch(*app.gameState.Candidate, app.settings.Launch); err != nil {
			app.launchUIError = err.Error()
		}
	case y >= sy(220) && y < sy(258):
		next := app.settings.Launch
		next.WindowMode = (next.WindowMode + 1) % 4
		app.commitLaunchConfig(next)
	case y >= sy(264) && y < sy(302):
		presets := [][2]int{{1280, 720}, {1920, 1080}, {2560, 1440}, {3840, 2160}, {0, 0}}
		index := 0
		for i, preset := range presets {
			if preset[0] == app.settings.Launch.Width && preset[1] == app.settings.Launch.Height {
				index = (i + 1) % len(presets)
				break
			}
		}
		next := app.settings.Launch
		next.Width, next.Height = presets[index][0], presets[index][1]
		app.commitLaunchConfig(next)
	case y >= sy(308) && y < sy(346):
		client := win32.GetClientRect(app.hwnd)
		left, right := win32.Scale(252, app.dpi), client.Right-win32.Scale(42, app.dpi)
		midpoint := left + (right-left)/2
		monitorRect := win32.Rect{Left: left, Top: win32.Scale(308, app.dpi), Right: midpoint - win32.Scale(4, app.dpi), Bottom: win32.Scale(346, app.dpi)}
		postRect := win32.Rect{Left: midpoint + win32.Scale(4, app.dpi), Top: monitorRect.Top, Right: right, Bottom: monitorRect.Bottom}
		next := app.settings.Launch
		if pointInButton(monitorRect, int32(x), int32(y)) {
			next.Monitor = (next.Monitor + 1) % (win32.MonitorCount() + 1)
		} else if pointInButton(postRect, int32(x), int32(y)) {
			next.PostBehavior = (next.PostBehavior + 1) % 3
		} else {
			break
		}
		app.commitLaunchConfig(next)
	case y >= sy(396) && y < sy(434):
		contentLeft := win32.Scale(252, app.dpi)
		clientRight := win32.GetClientRect(app.hwnd).Right - win32.Scale(42, app.dpi)
		actionWidth := (clientRight - contentLeft) / 3
		column := -1
		for index := 0; index < 3; index++ {
			rect := win32.Rect{Left: contentLeft + int32(index)*actionWidth, Top: win32.Scale(396, app.dpi), Right: contentLeft + int32(index+1)*actionWidth - win32.Scale(6, app.dpi), Bottom: win32.Scale(434, app.dpi)}
			if pointInButton(rect, int32(x), int32(y)) {
				column = index
				break
			}
		}
		switch column {
		case 0:
			if app.gameState.Scanning {
				app.tasks.Cancel(app.gameTask)
				app.gameState.Scanning = false
				app.gameState.Status = app.texts.Text("game.status.scanCanceled")
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
			settings := app.settings
			settings.Game.Path = candidate.Root
			settings.Game.CustomExecutable = ""
			if !strings.EqualFold(candidate.ExeName, "YuanShen.exe") && !strings.EqualFold(candidate.ExeName, "GenshinImpact.exe") {
				settings.Game.CustomExecutable = candidate.ExeName
			}
			if err := config.Save(app.layout.Config, settings); err != nil {
				app.gameState.Error = fmt.Sprintf(app.texts.Text("game.status.savePathFailed"), err)
				win32.Invalidate(app.hwnd)
				return
			}
			app.settings = settings
			app.startGameScan(candidate.Root)
		case 2:
			if app.gameState.Candidate == nil || !app.syncLaunchConfig() {
				app.shortcutStatus = app.texts.Text("game.status.selectGame")
				break
			}
			path, err := launch.CreateDesktopShortcut(app.texts.Text("game.shortcut.name"), *app.gameState.Candidate, app.settings.Launch)
			if err != nil {
				app.shortcutStatus = fmt.Sprintf(app.texts.Text("game.status.shortcutFailed"), err)
			} else {
				app.shortcutStatus = fmt.Sprintf(app.texts.Text("game.status.shortcutCreated"), path)
			}
		}
	}
	win32.Invalidate(app.hwnd)
}

func (app *application) startGameScan(manualRoot string) {
	if app.gameTask != 0 {
		app.tasks.Cancel(app.gameTask)
	}
	app.gameState = gameViewState{Scanning: true, Status: app.texts.Text("game.status.scanning")}
	win32.Invalidate(app.hwnd)
	gameSettings := app.settings.Game
	texts := app.texts
	taskID := app.tasks.Run(func(ctx context.Context, id uint64) {
		state := gameViewState{Scanning: true, Status: texts.Text("game.status.validating")}
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
				state.Status = fmt.Sprintf(texts.Text("game.status.multiple"), state.CandidateCount)
			} else {
				state.Status = texts.Text("game.status.notFound")
			}
			publish()
			return
		}
		state.Candidate = &candidate
		state.Status = texts.Text("game.status.calculating")
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
		state.Status = texts.Text("game.status.complete")
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
		app.paintButtonSurface(dc, rect, brush)
		return rect
	}
	staticRow := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		app.paintStaticSurface(dc, rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	launchText := app.texts.Text("game.launch.clean")
	if app.launchSnap.State == launch.StateStarting {
		launchText = app.texts.Text("game.launch.starting")
	} else if app.launchSnap.State == launch.StateRunning {
		launchText = fmt.Sprintf(app.texts.Text("game.launch.running"), app.launchSnap.PID)
	}
	draw(launchText, row(170, 214, accentBrush), win32.Color(235, 238, 248))
	draw(fmt.Sprintf(app.texts.Text("game.windowMode"), gameWindowModeText(app.texts, app.settings.Launch.WindowMode)), row(220, 258, buttonBrush), win32.Color(225, 229, 242))
	resolution := app.texts.Text("game.window.default")
	if app.settings.Launch.Width > 0 {
		resolution = fmt.Sprintf("%d × %d", app.settings.Launch.Width, app.settings.Launch.Height)
	}
	draw(fmt.Sprintf(app.texts.Text("game.resolution"), resolution), row(264, 302, buttonBrush), win32.Color(225, 229, 242))
	midpoint := left + (right-left)/2
	monitorRect := win32.Rect{Left: left, Top: win32.Scale(308, app.dpi), Right: midpoint - win32.Scale(4, app.dpi), Bottom: win32.Scale(346, app.dpi)}
	postRect := win32.Rect{Left: midpoint + win32.Scale(4, app.dpi), Top: monitorRect.Top, Right: right, Bottom: monitorRect.Bottom}
	app.paintButtonSurface(dc, monitorRect, buttonBrush)
	app.paintButtonSurface(dc, postRect, buttonBrush)
	monitor := app.texts.Text("game.monitor.default")
	if app.settings.Launch.Monitor > 0 {
		monitor = fmt.Sprintf(app.texts.Text("game.monitor.number"), app.settings.Launch.Monitor)
	}
	postNames := []string{app.texts.Text("game.post.keep"), app.texts.Text("game.post.minimize"), app.texts.Text("game.post.exit")}
	draw(fmt.Sprintf(app.texts.Text("game.target"), monitor), monitorRect, win32.Color(225, 229, 242))
	draw(fmt.Sprintf(app.texts.Text("game.after"), postNames[app.settings.Launch.PostBehavior]), postRect, win32.Color(225, 229, 242))

	actionWidth := (right - left) / 3
	actions := []string{app.texts.Text("game.action.scan"), app.texts.Text("game.action.select"), app.texts.Text("game.action.shortcut")}
	for index, text := range actions {
		rect := win32.Rect{Left: left + int32(index)*actionWidth, Top: win32.Scale(396, app.dpi), Right: left + int32(index+1)*actionWidth - win32.Scale(6, app.dpi), Bottom: win32.Scale(434, app.dpi)}
		app.paintButtonSurface(dc, rect, buttonBrush)
		draw(text, rect, win32.Color(190, 197, 216))
	}

	state := app.gameState
	status := state.Status
	statusColor := win32.Color(145, 154, 180)
	if app.launchUIError != "" {
		status, statusColor = fmt.Sprintf(app.texts.Text("game.error.launch"), app.launchUIError), win32.Color(255, 126, 126)
	} else if app.shortcutStatus != "" {
		status = app.shortcutStatus
	} else if app.launchSnap.State == launch.StateExited {
		status = fmt.Sprintf(app.texts.Text("game.exited"), app.launchSnap.ExitCode)
	} else if app.launchSnap.State == launch.StateFailed {
		status, statusColor = fmt.Sprintf(app.texts.Text("game.error.process"), app.launchSnap.LastError), win32.Color(255, 126, 126)
	} else if state.Error != "" {
		status, statusColor = state.Error, win32.Color(255, 126, 126)
	}
	draw(status, staticRow(440, 476, cardBrush), statusColor)
	if state.Candidate == nil {
		return
	}
	candidate := state.Candidate
	draw(fmt.Sprintf(app.texts.Text("game.path"), candidate.Root), staticRow(482, 518, cardBrush), win32.Color(225, 229, 242))
	draw(fmt.Sprintf(app.texts.Text("game.identity"), candidate.ExeName, localizedValueOrUnknown(app.texts, candidate.Version), gameServerText(app.texts, candidate.Server)), staticRow(524, 560, cardBrush), win32.Color(190, 197, 216))
	running := app.texts.Text("game.running.none")
	if len(state.Running) > 0 {
		if state.Running[0].VerifiedPath {
			running = fmt.Sprintf(app.texts.Text("game.running.verified"), state.Running[0].PID)
		} else {
			running = fmt.Sprintf(app.texts.Text("game.running.possible"), state.Running[0].PID)
		}
	}
	draw(fmt.Sprintf(app.texts.Text("game.files"), formatBytes(state.Size.Bytes), state.Size.Files, state.Skipped, running), staticRow(566, 602, cardBrush), win32.Color(166, 174, 197))
}

func gameWindowModeText(texts localization.Catalog, mode launch.WindowMode) string {
	keys := []string{"game.window.default", "game.window.fullscreen", "game.window.windowed", "game.window.borderless"}
	if int(mode) < 0 || int(mode) >= len(keys) {
		return texts.Text("game.window.default")
	}
	return texts.Text(keys[mode])
}

func gameServerText(texts localization.Catalog, server game.Server) string {
	key := "game.server.unknown"
	switch server {
	case game.ServerCNOfficial:
		key = "game.server.official"
	case game.ServerCNBilibili:
		key = "game.server.bilibili"
	case game.ServerGlobal:
		key = "game.server.global"
	}
	return texts.Text(key)
}

func localizedValueOrUnknown(texts localization.Catalog, value string) string {
	if strings.TrimSpace(value) == "" {
		return texts.Text("common.unknown")
	}
	return value
}

func (app *application) resourceClick(_, y int) {
	sy := func(value int32) int { return int(win32.Scale(value, app.dpi)) }
	state := app.resourceState
	if state.Busy {
		if y >= sy(170) && y < sy(214) {
			app.tasks.Cancel(app.resourceTask)
			app.resourceState.Status = app.texts.Text("resource.status.canceling")
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
		state.Status = app.texts.Text("resource.status.voiceChanged")
		state.Error = ""
		app.resourceState = state
	case y >= sy(264) && y < sy(302):
		state.PreDownload = !state.PreDownload
		state.HasPlan = false
		state.Confirm = false
		state.Status = app.texts.Text("resource.status.branchChanged")
		state.Error = ""
		app.resourceState = state
	case y >= sy(308) && y < sy(352):
		if !state.HasPlan {
			state.Error = app.texts.Text("resource.error.needPlan")
		} else if len(state.Plan.Changes()) == 0 && state.PreDownload {
			state.Status = app.texts.Text("resource.status.noChanges")
			state.Error = ""
		} else if len(app.gameState.Running) > 0 {
			state.Error = app.texts.Text("resource.error.gameRunning")
		} else if !state.Confirm {
			state.Confirm = true
			state.Status = app.texts.Text("resource.status.confirm")
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
		app.resourceState.Error = app.texts.Text("resource.error.selectGame")
		win32.Invalidate(app.hwnd)
		return
	}
	root := app.gameState.Candidate.Root
	state := app.resourceState
	state.Busy, state.Confirm, state.HasPlan = true, false, false
	state.Error = ""
	state.Status = app.texts.Text("resource.status.reading")
	app.resourceState = state
	texts := app.texts
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
					err = errors.New(texts.Text("resource.error.noPreload"))
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
			state.Status = texts.Text("resource.status.manifest")
			publish(id, state, false)
			state.Manifest, err = provider.LoadManifest(ctx, catalog, "game", state.Language)
			state.Version = catalog.Version
		}
		if err == nil {
			state.Status = texts.Text("resource.status.planning")
			publish(id, state, false)
			state.Plan, err = resources.BuildRepairPlanContext(ctx, root, state.Manifest)
		}
		state.Busy = false
		if err != nil {
			if errors.Is(err, context.Canceled) {
				state.Status, state.Error = texts.Text("resource.status.canceled"), ""
			} else {
				state.Error = err.Error()
				state.Status = texts.Text("resource.status.failed")
			}
		} else {
			state.HasPlan = true
			state.Status = fmt.Sprintf(texts.Text("resource.status.planComplete"), len(state.Plan.Changes()))
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
	state.Status = app.texts.Text("resource.status.preparing")
	app.resourceState = state
	texts := app.texts
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
			state.Status = texts.Text("resource.status.downloading")
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
				state.Status, state.Error = texts.Text("resource.status.preloadFailed"), err.Error()
			} else {
				state.HasPlan = false
				state.Status = texts.Text("resource.status.preloaded")
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
			state.Status = texts.Text("resource.status.committing")
			publish(id, state, false)
			err = transaction.Commit(state.Plan)
		}
		state.Busy = false
		if err != nil {
			if errors.Is(err, context.Canceled) {
				state.Status, state.Error = texts.Text("resource.status.taskCanceled"), ""
			} else {
				state.Status = texts.Text("resource.status.txFailed")
				state.Error = err.Error()
			}
			publish(id, state, false)
			return
		}
		state.HasPlan = false
		state.Status = texts.Text("resource.status.complete")
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
		app.paintButtonSurface(dc, rect, brush)
		return rect
	}
	staticRow := func(top, bottom int32, brush win32.HBRUSH) win32.Rect {
		rect := win32.Rect{Left: left, Top: win32.Scale(top, app.dpi), Right: right, Bottom: win32.Scale(bottom, app.dpi)}
		app.paintStaticSurface(dc, rect, brush)
		return rect
	}
	draw := func(text string, rect win32.Rect, color uint32) {
		win32.SetTextColor(dc, color)
		rect.Left += win32.Scale(18, app.dpi)
		rect.Right -= win32.Scale(12, app.dpi)
		win32.DrawText(dc, text, &rect, win32.DT_LEFT|win32.DT_VCENTER|win32.DT_SINGLELINE|win32.DT_END_ELLIPSIS)
	}
	state := app.resourceState
	checkText := app.texts.Text("resource.check")
	if state.Busy {
		checkText = app.texts.Text("resource.cancel")
	}
	draw(checkText, row(170, 214, accentBrush), win32.Color(235, 238, 248))
	draw(fmt.Sprintf(app.texts.Text("resource.voice"), state.Language), row(220, 258, buttonBrush), win32.Color(225, 229, 242))
	branch := app.texts.Text("resource.branch.current")
	if state.PreDownload {
		branch = app.texts.Text("resource.branch.preload")
	}
	draw(fmt.Sprintf(app.texts.Text("resource.branch"), branch), row(264, 302, buttonBrush), win32.Color(225, 229, 242))
	applyText := app.texts.Text("resource.apply.needPlan")
	if state.HasPlan {
		applyText = fmt.Sprintf(app.texts.Text("resource.apply.count"), len(state.Plan.Changes()))
	}
	if state.Confirm {
		applyText = app.texts.Text("resource.apply.confirm")
	}
	draw(applyText, row(308, 352, buttonBrush), win32.Color(225, 229, 242))
	statusColor := win32.Color(166, 174, 197)
	status := state.Status
	if state.Error != "" {
		status, statusColor = state.Error, win32.Color(255, 126, 126)
	}
	draw(status, staticRow(364, 408, cardBrush), statusColor)
	version := localizedValueOrUnknown(app.texts, state.Version)
	draw(fmt.Sprintf(app.texts.Text("resource.onlineVersion"), version), staticRow(414, 450, cardBrush), win32.Color(190, 197, 216))
	if state.HasPlan {
		draw(fmt.Sprintf(app.texts.Text("resource.planSummary"), len(state.Manifest.Files), len(state.Plan.Changes()), formatBytes(uint64(state.Plan.DownloadBytes))), staticRow(456, 492, cardBrush), win32.Color(190, 197, 216))
	}
	if state.Progress.FilesTotal > 0 {
		eta := app.texts.Text("resource.eta.calculating")
		if state.Progress.ETA > 0 {
			eta = state.Progress.ETA.Round(time.Second).String()
		}
		draw(fmt.Sprintf(app.texts.Text("resource.progress"), state.Progress.FilesDone, state.Progress.FilesTotal, formatBytes(uint64(state.Progress.BytesDone)), formatBytes(uint64(state.Progress.BytesTotal)), formatBytes(uint64(state.Progress.Speed)), eta), staticRow(498, 534, cardBrush), win32.Color(145, 154, 180))
	}
	draw(app.texts.Text("resource.safety"), staticRow(540, 576, cardBrush), win32.Color(126, 136, 160))
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
		cpuSampler := cpumonitor.NewSampler(runtime.NumCPU())
		var sustained cpumonitor.Sustained
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			now := time.Now()
			cpuTime, cpuErr := win32.CurrentProcessCPUTime()
			percent, valid := 0.0, false
			if cpuErr == nil {
				percent, valid = cpuSampler.Sample(now, cpuTime)
			}
			warning := app.cpuWarning.Load()
			if warning == nil {
				warning = &cpuWarningConfig{}
			}
			snapshot := diagnosticSnapshot{
				Resources:  win32.SnapshotResources(),
				CPUPercent: percent,
				CPUValid:   valid,
				CPUAlert:   sustained.Observe(now, percent, valid, warning.Enabled, warning.Threshold, time.Duration(warning.DurationMS)*time.Millisecond),
			}
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
