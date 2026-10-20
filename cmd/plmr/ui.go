package main

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ---------- цвета ----------

const (
	reset   = "\x1b[0m"
	bold    = "\x1b[1m"
	dim     = "\x1b[2m"
	under   = "\x1b[4m"
	red     = "\x1b[31m"
	green   = "\x1b[32m"
	yellow  = "\x1b[33m"
	blue    = "\x1b[34m"
	magenta = "\x1b[35m"
	cyan    = "\x1b[36m"
	white   = "\x1b[37m"
	bred    = "\x1b[1;31m"
	bgreen  = "\x1b[1;32m"
	byellow = "\x1b[1;33m"
	bblue   = "\x1b[1;34m"
	bgRed   = "\x1b[41;1;37m"
)

func c(color, s string) string { return color + s + reset }

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func visLen(s string) int { return utf8.RuneCountInString(ansiRe.ReplaceAllString(s, "")) }

func pad(s string, w int) string {
	if n := visLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func padL(s string, w int) string {
	if n := visLen(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// ---------- статусные строки в духе msf ----------

func info(f string, a ...any) { fmt.Printf(c(bblue, "[*]")+" "+f+"\n", a...) }
func good(f string, a ...any) { fmt.Printf(c(bgreen, "[+]")+" "+f+"\n", a...) }
func bad(f string, a ...any)  { fmt.Printf(c(bred, "[-]")+" "+f+"\n", a...) }
func warn(f string, a ...any) { fmt.Printf(c(byellow, "[!]")+" "+f+"\n", a...) }

// ---------- таблицы ----------

type table struct {
	title   string
	headers []string
	rows    [][]string
	right   map[int]bool // колонки с выравниванием вправо
}

func (t *table) add(cells ...string) { t.rows = append(t.rows, cells) }

func (t *table) print() {
	if t.title != "" {
		fmt.Println()
		fmt.Println(c(bold, t.title))
		fmt.Println(c(bold, strings.Repeat("=", visLen(t.title))))
	}
	fmt.Println()
	w := make([]int, len(t.headers))
	for i, h := range t.headers {
		w[i] = visLen(h)
	}
	for _, r := range t.rows {
		for i, cell := range r {
			if i < len(w) && visLen(cell) > w[i] {
				w[i] = visLen(cell)
			}
		}
	}
	line := func(cells []string, under bool) {
		var b strings.Builder
		b.WriteString("  ")
		for i := range w {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			if under {
				cell = strings.Repeat("-", visLen(t.headers[i]))
			}
			if t.right[i] {
				b.WriteString(padL(cell, w[i]))
			} else {
				b.WriteString(pad(cell, w[i]))
			}
			if i < len(w)-1 {
				b.WriteString("  ")
			}
		}
		fmt.Println(strings.TrimRight(b.String(), " "))
	}
	line(t.headers, false)
	line(nil, true)
	for _, r := range t.rows {
		line(r, false)
	}
	fmt.Println()
}

// kv - таблица «параметр / значение», как `show options`
func kv(title string, pairs ...[2]string) {
	t := &table{title: title, headers: []string{"Name", "Value"}}
	for _, p := range pairs {
		t.add(p[0], p[1])
	}
	t.print()
}

// ---------- спиннер ----------

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spin крутит индикатор, пока идёт сетевой вызов; done(err) печатает [+]/[-]
func spin(msg string) (done func(err error)) {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	start := time.Now()
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				fmt.Print("\r\x1b[2K")
				return
			case <-time.After(80 * time.Millisecond):
				fmt.Printf("\r%s %s", c(cyan, spinFrames[i%len(spinFrames)]), msg)
			}
		}
	}()
	return func(err error) {
		close(stop)
		wg.Wait()
		ms := c(dim, fmt.Sprintf("(%d ms)", time.Since(start).Milliseconds()))
		if err != nil {
			bad("%s — %v %s", msg, err, ms)
		} else {
			good("%s %s", msg, ms)
		}
	}
}

// ---------- баннеры ----------

var tips = []string{
	"Используйте " + c(bold, "quote") + " перед " + c(bold, "buy") + " — увидите полную цену круга до выстрела.",
	"Tab дополняет команды и тикеры, стрелки листают историю.",
	c(bold, "watch") + " показывает стакан вживую — Enter для выхода.",
	"Цена должна пройти спред + 2 комиссии, прежде чем вы заработаете хоть копейку.",
	"Рыночная заявка платит спред всегда. Лимитная — иногда не исполняется.",
	c(bold, "use SBER@TQBR") + " — явный выбор режима торгов, если тикер неоднозначен.",
	"Журнал сделок пишется в journal.jsonl — его читает " + c(bold, "report") + ".",
	"Токен даёт доступ к деньгам. Не светите его в логах и чатах.",
}

// splash - блок «=[ ... ]» как у msfconsole
func splash(lines []string) {
	w := 0
	for _, l := range lines {
		if visLen(l) > w {
			w = visLen(l)
		}
	}
	fmt.Println()
	for i, l := range lines {
		prefix := "+ -- --=[ "
		if i == 0 {
			prefix = "       =[ "
		}
		fmt.Printf("%s%s ]\n", prefix, pad(l, w))
	}
	fmt.Println()
	fmt.Println(c(dim, "Tip: ") + tips[rand.Intn(len(tips))])
	fmt.Println()
}

// typeOut печатает строку «печатной машинкой»
func typeOut(s string, d time.Duration) {
	for _, r := range s {
		fmt.Print(string(r))
		time.Sleep(d)
	}
	fmt.Println()
}

func liveBadge(live bool) string {
	if live {
		return c(bgRed, " LIVE ")
	}
	return c("\x1b[42;1;30m", " SANDBOX ")
}

// pnl красит число по знаку
func pnl(v float64, cur string) string {
	s := fmt.Sprintf("%+.2f %s", v, cur)
	switch {
	case v > 0:
		return c(bgreen, s)
	case v < 0:
		return c(bred, s)
	}
	return s
}

// bar - полоска объёма для стакана
func bar(qty, max int64, width int, color string) string {
	if max <= 0 {
		return ""
	}
	n := int(float64(qty) / float64(max) * float64(width))
	if n < 1 && qty > 0 {
		n = 1
	}
	return c(color, strings.Repeat("█", n)) + c(dim, strings.Repeat("░", width-n))
}
