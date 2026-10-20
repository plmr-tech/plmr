package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVT включает обработку ANSI-последовательностей в консоли Windows
func enableVT() {
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		h := windows.Handle(f.Fd())
		var mode uint32
		if windows.GetConsoleMode(h, &mode) == nil {
			_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
		}
	}
	_ = windows.SetConsoleOutputCP(65001)
}
