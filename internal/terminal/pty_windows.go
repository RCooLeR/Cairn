//go:build windows

package terminal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procUpdateProcThreadAttribute = windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")

type windowsPTYStarter struct{}

type windowsPTYSession struct {
	processMu   sync.Mutex
	lifecycleMu sync.RWMutex
	process     windows.Handle
	thread      windows.Handle
	console     windows.Handle
	attr        *windows.ProcThreadAttributeListContainer
	input       *os.File
	output      *os.File

	closeOnce sync.Once
	waitOnce  sync.Once
	waitCode  int
}

func newDefaultPTYStarter() PTYStarter {
	return windowsPTYStarter{}
}

func (windowsPTYStarter) Start(_ context.Context, spec PTYSpec) (PTYSession, error) {
	if len(spec.Argv) == 0 || strings.TrimSpace(spec.Argv[0]) == "" {
		return nil, errors.New("terminal argv is empty")
	}
	cols, rows := normalizeDimensions(spec.Cols, spec.Rows)
	console, input, output, attr, err := createPseudoConsole(cols, rows)
	if err != nil {
		return nil, err
	}

	process, thread, err := createPseudoConsoleProcess(spec, attr)
	if err != nil {
		attr.Delete()
		windows.ClosePseudoConsole(console)
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}

	session := &windowsPTYSession{
		process:  process,
		thread:   thread,
		console:  console,
		attr:     attr,
		input:    input,
		output:   output,
		waitCode: -1,
	}
	return session, nil
}

func (s *windowsPTYSession) Read(p []byte) (int, error) {
	output := s.outputFile()
	if output == nil {
		return 0, os.ErrClosed
	}
	return output.Read(p)
}

func (s *windowsPTYSession) Write(p []byte) (int, error) {
	input := s.inputFile()
	if input == nil {
		return 0, os.ErrClosed
	}
	return input.Write(p)
}

func (s *windowsPTYSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.lifecycleMu.Lock()
		input := s.input
		output := s.output
		console := s.console
		attr := s.attr
		thread := s.thread
		s.input = nil
		s.output = nil
		s.console = 0
		s.attr = nil
		s.thread = 0
		if input != nil {
			err = errors.Join(err, input.Close())
		}
		if output != nil {
			err = errors.Join(err, output.Close())
		}
		if console != 0 {
			windows.ClosePseudoConsole(console)
		}
		if attr != nil {
			attr.Delete()
		}
		if thread != 0 {
			err = errors.Join(err, windows.CloseHandle(thread))
		}
		s.lifecycleMu.Unlock()

		if process := s.processHandle(); process != 0 {
			if exited := waitForProcess(process, 0); !exited {
				_ = windows.TerminateProcess(process, 1)
				time.Sleep(200 * time.Millisecond)
			}
		}
	})
	return err
}

func (s *windowsPTYSession) Resize(cols int, rows int) error {
	cols, rows = normalizeDimensions(cols, rows)
	s.lifecycleMu.RLock()
	defer s.lifecycleMu.RUnlock()
	if s.console == 0 {
		return os.ErrClosed
	}
	return windows.ResizePseudoConsole(s.console, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (s *windowsPTYSession) inputFile() *os.File {
	s.lifecycleMu.RLock()
	defer s.lifecycleMu.RUnlock()
	return s.input
}

func (s *windowsPTYSession) outputFile() *os.File {
	s.lifecycleMu.RLock()
	defer s.lifecycleMu.RUnlock()
	return s.output
}

func (s *windowsPTYSession) Wait() int {
	s.waitOnce.Do(func() {
		process := s.processHandle()
		if process == 0 {
			s.waitCode = -1
			return
		}
		if _, err := windows.WaitForSingleObject(process, windows.INFINITE); err != nil {
			s.waitCode = -1
			return
		}
		var exitCode uint32
		if err := windows.GetExitCodeProcess(process, &exitCode); err != nil {
			s.waitCode = -1
			return
		}
		s.waitCode = int(exitCode)
		s.closeProcessHandle(process)
	})
	return s.waitCode
}

func (s *windowsPTYSession) processHandle() windows.Handle {
	s.processMu.Lock()
	defer s.processMu.Unlock()
	return s.process
}

func (s *windowsPTYSession) closeProcessHandle(process windows.Handle) {
	s.processMu.Lock()
	defer s.processMu.Unlock()
	if s.process != process || s.process == 0 {
		return
	}
	_ = windows.CloseHandle(s.process)
	s.process = 0
}

func createPseudoConsole(cols int, rows int) (windows.Handle, *os.File, *os.File, *windows.ProcThreadAttributeListContainer, error) {
	var ptyInputRead windows.Handle
	var ptyInputWrite windows.Handle
	if err := windows.CreatePipe(&ptyInputRead, &ptyInputWrite, nil, 0); err != nil {
		return 0, nil, nil, nil, err
	}

	var ptyOutputRead windows.Handle
	var ptyOutputWrite windows.Handle
	if err := windows.CreatePipe(&ptyOutputRead, &ptyOutputWrite, nil, 0); err != nil {
		_ = windows.CloseHandle(ptyInputRead)
		_ = windows.CloseHandle(ptyInputWrite)
		return 0, nil, nil, nil, err
	}

	var console windows.Handle
	err := windows.CreatePseudoConsole(
		windows.Coord{X: int16(cols), Y: int16(rows)},
		ptyInputRead,
		ptyOutputWrite,
		0,
		&console,
	)
	_ = windows.CloseHandle(ptyInputRead)
	_ = windows.CloseHandle(ptyOutputWrite)
	if err != nil {
		_ = windows.CloseHandle(ptyInputWrite)
		_ = windows.CloseHandle(ptyOutputRead)
		return 0, nil, nil, nil, err
	}

	attr, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(console)
		_ = windows.CloseHandle(ptyInputWrite)
		_ = windows.CloseHandle(ptyOutputRead)
		return 0, nil, nil, nil, err
	}
	if err := updatePseudoConsoleAttribute(attr, console); err != nil {
		attr.Delete()
		windows.ClosePseudoConsole(console)
		_ = windows.CloseHandle(ptyInputWrite)
		_ = windows.CloseHandle(ptyOutputRead)
		return 0, nil, nil, nil, err
	}

	return console,
		os.NewFile(uintptr(ptyInputWrite), "|cairn-conpty-input"),
		os.NewFile(uintptr(ptyOutputRead), "|cairn-conpty-output"),
		attr,
		nil
}

func updatePseudoConsoleAttribute(attr *windows.ProcThreadAttributeListContainer, console windows.Handle) error {
	if err := procUpdateProcThreadAttribute.Find(); err != nil {
		return err
	}
	r1, _, e1 := syscall.SyscallN(
		procUpdateProcThreadAttribute.Addr(),
		uintptr(unsafe.Pointer(attr.List())),
		0,
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(console),
		unsafe.Sizeof(console),
		0,
		0,
	)
	if r1 != 0 {
		return nil
	}
	if e1 != 0 {
		return e1
	}
	return syscall.EINVAL
}

func createPseudoConsoleProcess(spec PTYSpec, attr *windows.ProcThreadAttributeListContainer) (windows.Handle, windows.Handle, error) {
	executable, err := exec.LookPath(spec.Argv[0])
	if err != nil {
		return 0, 0, err
	}
	appName, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return 0, 0, err
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{executable}, spec.Argv[1:]...)))
	if err != nil {
		return 0, 0, err
	}
	var currentDir *uint16
	if cwd := windowsProcessWorkingDir(spec.WorkingDir); cwd != "" {
		currentDir, err = windows.UTF16PtrFromString(cwd)
		if err != nil {
			return 0, 0, err
		}
	}
	envBlock, err := windowsEnvironmentBlock(spec.Env)
	if err != nil {
		return 0, 0, err
	}

	siEx := &windows.StartupInfoEx{}
	siEx.Cb = uint32(unsafe.Sizeof(*siEx))
	siEx.Flags = windows.STARTF_USESTDHANDLES
	siEx.ProcThreadAttributeList = attr.List()

	pi := &windows.ProcessInformation{}
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	err = windows.CreateProcess(
		appName,
		commandLine,
		&windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1},
		&windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1},
		false,
		flags,
		&envBlock[0],
		currentDir,
		&siEx.StartupInfo,
		pi,
	)
	if err != nil {
		return 0, 0, err
	}
	return pi.Process, pi.Thread, nil
}

func windowsProcessWorkingDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func windowsEnvironmentBlock(env map[string]string) ([]uint16, error) {
	merged := make(map[string]string)
	canonical := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		folded := strings.ToUpper(key)
		canonical[folded] = key
		merged[key] = value
	}
	for key, value := range env {
		key = strings.TrimSpace(key)
		if key == "" || strings.Contains(key, "\x00") || strings.Contains(value, "\x00") {
			return nil, syscall.EINVAL
		}
		folded := strings.ToUpper(key)
		if previous, ok := canonical[folded]; ok && previous != key {
			delete(merged, previous)
		}
		canonical[folded] = key
		merged[key] = value
	}
	entries := make([]string, 0, len(merged))
	for key, value := range merged {
		entries = append(entries, key+"="+value)
	}
	sort.Strings(entries)
	if len(entries) == 0 {
		return []uint16{0, 0}, nil
	}
	return utf16.Encode([]rune(strings.Join(entries, "\x00") + "\x00\x00")), nil
}

func waitForProcess(process windows.Handle, timeout uint32) bool {
	event, err := windows.WaitForSingleObject(process, timeout)
	return err == nil && event == windows.WAIT_OBJECT_0
}
