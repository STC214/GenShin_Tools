//go:build windows

package shell

import (
	"reflect"
	"testing"

	"genshintools/internal/localization"
)

func TestTrayMenuExposesHomeLaunchActions(t *testing.T) {
	app := application{texts: localization.New(localization.ZH, "")}
	items := app.trayMenuItems()
	if len(items) != 6 {
		t.Fatalf("tray item count = %d, want 6", len(items))
	}
	if items[2].ID != menuInjection || items[2].Text != "注入启动" {
		t.Fatalf("injection tray item = %+v", items[2])
	}
	if items[3].ID != menuClean || items[3].Text != "纯净启动" {
		t.Fatalf("clean tray item = %+v", items[3])
	}
	if !items[1].Separator || !items[4].Separator {
		t.Fatalf("tray separators missing: %+v", items)
	}
}

func TestTrayLaunchCommandsDispatchToHomeLaunchMethods(t *testing.T) {
	tests := []struct {
		name       string
		command    uintptr
		want       func(*application)
		invalidate bool
	}{
		{name: "injection", command: menuInjection, want: (*application).startInjectionLaunch, invalidate: true},
		{name: "clean", command: menuClean, want: (*application).startCleanLaunch, invalidate: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, ok := trayHandlerForCommand(test.command)
			if !ok {
				t.Fatalf("tray command %d has no handler", test.command)
			}
			if reflect.ValueOf(handler.run).Pointer() != reflect.ValueOf(test.want).Pointer() {
				t.Fatalf("tray command %d dispatches to the wrong application method", test.command)
			}
			if handler.invalidate != test.invalidate {
				t.Fatalf("tray command %d invalidate = %t, want %t", test.command, handler.invalidate, test.invalidate)
			}
		})
	}
}

func TestUnknownTrayCommandIsIgnored(t *testing.T) {
	if _, ok := trayHandlerForCommand(0); ok {
		t.Fatal("unknown tray command unexpectedly has a handler")
	}
}
