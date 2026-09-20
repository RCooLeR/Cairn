package shell

import (
	"reflect"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestRestoreMainWindowShowsRestoresAndFocuses(t *testing.T) {
	window := &fakeRestorableMainWindow{minimised: true}

	restoreMainWindow(window)

	want := []string{"show", "is-minimised", "unminimise", "focus"}
	if !reflect.DeepEqual(window.calls, want) {
		t.Fatalf("restoreMainWindow() calls = %#v, want %#v", window.calls, want)
	}
	if window.minimised {
		t.Fatal("restoreMainWindow() left the window minimised")
	}
}

func TestRestoreMainWindowDoesNotUnminimiseVisibleWindow(t *testing.T) {
	window := &fakeRestorableMainWindow{}

	restoreMainWindow(window)

	want := []string{"show", "is-minimised", "focus"}
	if !reflect.DeepEqual(window.calls, want) {
		t.Fatalf("restoreMainWindow() calls = %#v, want %#v", window.calls, want)
	}
}

func TestRestoreMainWindowAcceptsNil(t *testing.T) {
	restoreMainWindow(nil)
}

type fakeRestorableMainWindow struct {
	minimised bool
	calls     []string
}

func (w *fakeRestorableMainWindow) Show() application.Window {
	w.calls = append(w.calls, "show")
	return nil
}

func (w *fakeRestorableMainWindow) IsMinimised() bool {
	w.calls = append(w.calls, "is-minimised")
	return w.minimised
}

func (w *fakeRestorableMainWindow) UnMinimise() {
	w.calls = append(w.calls, "unminimise")
	w.minimised = false
}

func (w *fakeRestorableMainWindow) Focus() {
	w.calls = append(w.calls, "focus")
}
