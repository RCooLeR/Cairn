//go:build windows

package terminal

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPTYWaitReleasesHandleBeforeTermination(t *testing.T) {
	t.Parallel()
	// A signaled event lets the wait finish without starting another process.
	// Do not assume a particular GetExitCodeProcess result for this non-process
	// handle; Wait must release ownership regardless of that API's result.
	handle, err := windows.CreateEvent(nil, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	session := &windowsPTYSession{process: handle, waitCode: -1}
	t.Cleanup(func() { session.closeProcessHandle(handle) })
	session.Wait()
	if got := session.processHandle(); got != 0 {
		t.Fatalf("Wait retained process handle %d", got)
	}
	if session.terminateProcessIfRunning() {
		t.Fatal("termination attempted after Wait released process ownership")
	}
}
