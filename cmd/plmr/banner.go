package main

import (
	"fmt"
	"strings"
)

var glyphs = map[rune][]string{
	'P': {
		"██████╗ ",
		"██╔══██╗",
		"██████╔╝",
		"██╔═══╝ ",
		"██║     ",
		"╚═╝     ",
	},
	'L': {
		"██╗     ",
		"██║     ",
		"██║     ",
		"██║     ",
		"███████╗",
		"╚══════╝",
	},
	'M': {
		"███╗   ███╗",
		"████╗ ████║",
		"██╔████╔██║",
		"██║╚██╔╝██║",
		"██║ ╚═╝ ██║",
		"╚═╝     ╚═╝",
	},
	'R': {
		"██████╗ ",
		"██╔══██╗",
		"██████╔╝",
		"██╔══██╗",
		"██║  ██║",
		"╚═╝  ╚═╝",
	},
}

func printBanner() {
	fmt.Println()
	for row := 0; row < 6; row++ {
		var b strings.Builder
		for _, r := range "PLMR" {
			b.WriteString(glyphs[r][row])
		}
		fmt.Println("  " + c(green, b.String()))
	}
	fmt.Println(c(dim, "  trading console // T-Invest API"))
}
