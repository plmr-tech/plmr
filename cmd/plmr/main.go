// PLMR - торговая консоль: подключение, котировки, покупка/продажа
// и разбор всех издержек сделки (спред, комиссия).
//
//	plmr                              интерактивная консоль
//	plmr accounts                     счета, тариф, баланс
//	plmr quote  TICKER                стакан, спред, оценка комиссии
//	plmr buy    TICKER --yes          рыночная покупка (по умолчанию 1 лот)
//	plmr sell   TICKER --yes          рыночная продажа
//	plmr report TICKER [--since D]    итог по операциям
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/russianinvestments/invest-api-go-sdk/investgo"
)

const version = "0.1.0"

type quietLogger struct{}

func (quietLogger) Infof(string, ...any)      {}
func (quietLogger) Errorf(t string, a ...any) { bad(t, a...) }
func (quietLogger) Fatalf(t string, a ...any) { bad(t, a...); os.Exit(1) }

func main() {
	enableVT()
	log.SetFlags(0)

	cmd := ""
	rest := os.Args[1:]
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		cmd, rest = rest[0], rest[1:]
	}
	fs := flag.NewFlagSet("plmr", flag.ExitOnError)
	yes := fs.Bool("yes", false, "подтвердить реальную заявку")
	lots := fs.Int64("lots", 1, "количество лотов")
	since := fs.String("since", "", "начало периода для report (YYYY-MM-DD)")
	cfgPath := fs.String("config", "config.yaml", "файл конфигурации")
	quiet := fs.Bool("q", false, "без баннера")

	var args []string
	for len(rest) > 0 {
		if strings.HasPrefix(rest[0], "-") {
			_ = fs.Parse(rest)
			rest = fs.Args()
			continue
		}
		args = append(args, rest[0])
		rest = rest[1:]
	}

	if cmd == "" && !*quiet {
		printBanner()
	}

	conf, err := investgo.LoadConfig(*cfgPath)
	if err != nil {
		bad("Конфиг %s: %v", *cfgPath, err)
		os.Exit(1)
	}
	acc := conf.AccountId
	if acc == "" {
		// иначе NewClient полезет открывать счёт в песочнице
		conf.AccountId = "-"
	}
	if conf.EndPoint == "" {
		conf.EndPoint = "sandbox-invest-public-api.tbank.ru:443"
	}
	live := !strings.HasPrefix(conf.EndPoint, "sandbox")

	client, err := investgo.NewClient(context.Background(), conf, quietLogger{})
	if err != nil {
		bad("Клиент: %v", err)
		os.Exit(1)
	}
	defer client.Stop()

	a := &app{client: client, acc: acc, endpoint: conf.EndPoint, live: live, cfgPath: *cfgPath, lots: *lots}

	if cmd == "" {
		boot(a)
		a.console()
		return
	}

	a.confirm = func(string) bool {
		if !*yes {
			warn("Это реальная заявка. Повторите с %s, чтобы выставить.", c(bold, "--yes"))
		}
		return *yes
	}
	cmd = strings.ToLower(cmd)
	if cmd == "report" && *since != "" {
		args = append(args[:min(len(args), 1)], *since)
	}
	waitEnter := func() <-chan struct{} {
		stop := make(chan struct{})
		go func() {
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
			close(stop)
		}()
		return stop
	}
	if err := a.exec(cmd, args, waitEnter); err != nil {
		bad("%v", err)
		if errors.Is(err, errUnknown) {
			fmt.Fprintln(os.Stderr, "usage: plmr [команда консоли] — например: plmr show accounts | plmr quote SBER | plmr buy SBER --yes")
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// boot - заставка и рукопожатие с API
func boot(a *app) {
	typeOut(c(dim, "[*] Starting PLMR console..."), 8*time.Millisecond)
	done := spin("Handshake " + a.endpoint)
	infoResp, err := a.client.NewUsersServiceClient().GetInfo()
	done(err)
	tariff := c(dim, "?")
	if err == nil {
		tariff = infoResp.GetTariff()
	}
	acc := a.acc
	if acc == "" {
		acc = c(byellow, "not set") + " → show accounts / set ACCOUNT"
	}
	splash([]string{
		c(bold, "PLMR v"+version) + c(dim, "  // T-Invest API, go-sdk"),
		fmt.Sprintf("%d modules: quote watch buy sell report", 5),
		"endpoint " + a.endpoint + " " + liveBadge(a.live),
		"account  " + acc,
		"tariff   " + tariff,
	})
	if a.live {
		fmt.Println(c(bgRed, " !!! LIVE ACCOUNT — РЕАЛЬНЫЕ ДЕНЬГИ. Каждая заявка требует ввода 'yes'. !!! "))
		fmt.Println()
	}
}
