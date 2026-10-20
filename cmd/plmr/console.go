package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/chzyer/readline"
)

var popular = []string{"SBER", "GAZP", "LKOH", "YDEX", "T", "VTBR", "ROSN", "GMKN", "MOEX", "NVTK", "PLZL", "TMOS", "TRUR", "TGLD", "LQDT"}

type command struct {
	name, args, help string
}

var commands = []command{
	{"use", "<TICKER[@CLASS]>", "выбрать инструмент (цель)"},
	{"back", "", "сбросить цель"},
	{"info", "", "параметры выбранного инструмента"},
	{"quote", "[TICKER]", "стакан + полная цена круга (спред и комиссии)"},
	{"impact", "[TICKER] <сумма>", "средняя цена крупной рыночной заявки по стакану"},
	{"watch", "[TICKER]", "живой стакан, Enter — выход"},
	{"buy", "[TICKER]", "рыночная покупка LOTS лотов (с подтверждением)"},
	{"sell", "[TICKER]", "рыночная продажа LOTS лотов (с подтверждением)"},
	{"report", "[TICKER] [YYYY-MM-DD]", "разбор операций: P&L, комиссии, спред"},
	{"show", "accounts|balance|options|journal", "показать данные"},
	{"set", "LOTS <n> | ACCOUNT <id>", "задать параметр"},
	{"save", "", "записать ACCOUNT в config.yaml"},
	{"clear", "", "очистить экран"},
	{"help", "", "эта справка"},
	{"exit", "", "выход"},
}

func (a *app) prompt() string {
	p := c(under, "plmr")
	if a.target != nil {
		p += " ticker(" + c(bred, a.target.ticker) + ")"
	}
	if a.live {
		p = c(bred, "☠ ") + p
	}
	return p + " > "
}

func (a *app) console() {
	tickers := readline.PcItemDynamic(func(string) []string {
		out := append([]string{}, popular...)
		if a.target != nil {
			out = append([]string{a.target.ticker}, out...)
		}
		return out
	})
	completer := readline.NewPrefixCompleter(
		readline.PcItem("use", tickers),
		readline.PcItem("quote", tickers),
		readline.PcItem("impact", tickers),
		readline.PcItem("watch", tickers),
		readline.PcItem("buy", tickers),
		readline.PcItem("sell", tickers),
		readline.PcItem("report", tickers),
		readline.PcItem("show", readline.PcItem("accounts"), readline.PcItem("balance"), readline.PcItem("options"), readline.PcItem("journal")),
		readline.PcItem("set", readline.PcItem("LOTS"), readline.PcItem("ACCOUNT")),
		readline.PcItem("back"), readline.PcItem("info"), readline.PcItem("save"),
		readline.PcItem("clear"), readline.PcItem("help"), readline.PcItem("exit"),
	)
	home, _ := os.UserHomeDir()
	rl, err := readline.NewEx(&readline.Config{
		Prompt:            a.prompt(),
		AutoComplete:      completer,
		HistoryFile:       home + "/.plmr_history",
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
		HistorySearchFold: true,
	})
	if err != nil {
		bad("readline: %v", err)
		return
	}
	defer rl.Close()

	a.confirm = func(q string) bool {
		rl.SetPrompt(c(byellow, "[?] ") + q + " Введите " + c(bold, "yes") + ": ")
		defer rl.SetPrompt(a.prompt())
		line, err := rl.Readline()
		return err == nil && strings.TrimSpace(line) == "yes"
	}

	for {
		rl.SetPrompt(a.prompt())
		line, err := rl.Readline()
		if errors.Is(err, readline.ErrInterrupt) {
			if line == "" {
				info("Для выхода — " + c(bold, "exit"))
			}
			continue
		}
		if errors.Is(err, io.EOF) {
			return
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if strings.EqualFold(f[0], "exit") || strings.EqualFold(f[0], "quit") {
			info("Сессия закрыта. Happy trading.")
			return
		}
		waitEnter := func() <-chan struct{} {
			stop := make(chan struct{})
			rl.SetPrompt("")
			go func() {
				_, _ = rl.Readline()
				close(stop)
			}()
			return stop
		}
		if err := a.exec(strings.ToLower(f[0]), f[1:], waitEnter); err != nil {
			bad("%v", err)
		}
	}
}

var errUnknown = errors.New("неизвестная команда")

// exec выполняет одну команду — общая для консоли и разового запуска
func (a *app) exec(cmd string, args []string, waitEnter func() <-chan struct{}) error {
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch cmd {
	case "help", "?":
		printHelp()
	case "clear", "cls":
		fmt.Print("\x1b[2J\x1b[H")
	case "use":
		return a.use(arg(0))
	case "back":
		a.target = nil
	case "info":
		if arg(0) != "" {
			return a.use(arg(0))
		}
		a.instrumentInfo()
	case "quote":
		return a.quote(arg(0))
	case "impact":
		t, s := arg(0), arg(1)
		if s == "" {
			t, s = "", t
		}
		amount, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
		if err != nil || amount <= 0 {
			return fmt.Errorf("impact [TICKER] <сумма>, например impact SMLT 200000")
		}
		return a.impact(t, amount)
	case "watch":
		stop := waitEnter()
		err := a.watch(arg(0), stop)
		if err != nil {
			bad("%v", err)
			info("Нажмите Enter")
			<-stop
			return nil
		}
	case "buy", "sell":
		return a.trade(arg(0), cmd == "buy")
	case "report":
		t, d := arg(0), arg(1)
		if regexp.MustCompile(`^\d{4}-\d\d-\d\d$`).MatchString(t) {
			t, d = "", t
		}
		return a.report(t, d)
	case "accounts", "balance", "options", "journal":
		return a.show(cmd)
	case "show":
		return a.show(arg(0))
	case "set":
		return a.set(strings.ToUpper(arg(0)), arg(1))
	case "save":
		return a.save()
	default:
		return fmt.Errorf("%w: %s — наберите %s", errUnknown, cmd, c(bold, "help"))
	}
	return nil
}

func printHelp() {
	t := &table{title: "Core Commands", headers: []string{"Command", "Args", "Description"}}
	for _, cm := range commands {
		t.add(c(bold, cm.name), c(dim, cm.args), cm.help)
	}
	t.print()
}

func (a *app) show(what string) error {
	switch what {
	case "accounts":
		return a.accounts()
	case "balance":
		return a.balance()
	case "options", "":
		target := c(dim, "(none)")
		if a.target != nil {
			target = a.target.ticker + "@" + a.target.class
		}
		acc := a.acc
		if acc == "" {
			acc = c(byellow, "(not set)")
		}
		kv("Options (PLMR)",
			[2]string{"MODE", liveBadge(a.live)},
			[2]string{"ENDPOINT", a.endpoint},
			[2]string{"ACCOUNT", acc},
			[2]string{"TARGET", target},
			[2]string{"LOTS", fmt.Sprint(a.lots)},
			[2]string{"ORDER_TYPE", "MARKET"},
		)
	case "journal":
		t := &table{title: "Журнал заявок (" + journalFile + ")", headers: []string{"Time", "Side", "Ticker", "Lots", "Mid", "Exec", "Fee", "Latency"},
			right: map[int]bool{3: true, 4: true, 5: true, 6: true, 7: true}}
		for _, e := range readJournal() {
			side := c(green, e.Side)
			if e.Side == "SELL" {
				side = c(red, e.Side)
			}
			t.add(e.Time.Local().Format("02.01 15:04:05"), side, e.Ticker, fmt.Sprint(e.Lots), fp(e.Mid), fp(e.ExecPrice),
				fmt.Sprintf("%.2f", e.Commission), fmt.Sprintf("%d ms", e.LatencyMs))
		}
		if len(t.rows) == 0 {
			warn("Журнал пуст")
			return nil
		}
		t.print()
	default:
		return fmt.Errorf("show: accounts | balance | options | journal")
	}
	return nil
}

func (a *app) set(key, val string) error {
	switch key {
	case "LOTS":
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil || n < 1 {
			return fmt.Errorf("LOTS — целое число ≥ 1")
		}
		if n > 10 {
			warn("Многовато для пробы, но как скажете.")
		}
		a.lots = n
		good("LOTS => %d", n)
	case "ACCOUNT":
		if val == "" {
			return fmt.Errorf("set ACCOUNT <id>")
		}
		a.acc = val
		good("ACCOUNT => %s  %s", val, c(dim, "(save — записать в config.yaml)"))
	default:
		return fmt.Errorf("set LOTS <n> | ACCOUNT <id>")
	}
	return nil
}

func (a *app) save() error {
	data, err := os.ReadFile(a.cfgPath)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(?m)^AccountId:.*$`)
	line := fmt.Sprintf("AccountId: %q", a.acc)
	var out []byte
	if re.Match(data) {
		out = re.ReplaceAll(data, []byte(line))
	} else {
		out = append(data, []byte("\n"+line+"\n")...)
	}
	if err := os.WriteFile(a.cfgPath, out, 0o600); err != nil {
		return err
	}
	good("ACCOUNT сохранён в %s", a.cfgPath)
	return nil
}
