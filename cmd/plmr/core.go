package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/russianinvestments/invest-api-go-sdk/investgo"
	pb "github.com/russianinvestments/invest-api-go-sdk/proto"
)

const journalFile = "journal.jsonl"

type app struct {
	client   *investgo.Client
	acc      string
	endpoint string
	live     bool
	cfgPath  string
	lots     int64
	target   *instrument
	// confirm спрашивает подтверждение реальной заявки
	confirm func(prompt string) bool
}

func (a *app) needAccount() error {
	if a.acc == "" {
		return errors.New("ACCOUNT не задан — " + c(bold, "show accounts") + ", затем " + c(bold, "set ACCOUNT <id>"))
	}
	return nil
}

// ---------- счета ----------

func (a *app) accounts() error {
	us := a.client.NewUsersServiceClient()
	done := spin("Запрос профиля")
	infoResp, err := us.GetInfo()
	done(err)
	if err != nil {
		return err
	}
	done = spin("Запрос счетов")
	resp, err := us.GetAccounts(nil)
	done(err)
	if err != nil {
		return err
	}
	kv("Профиль",
		[2]string{"Tariff", infoResp.GetTariff()},
		[2]string{"Premium", fmt.Sprint(infoResp.GetPremStatus())},
		[2]string{"Qualified", fmt.Sprint(infoResp.GetQualStatus())},
	)
	t := &table{title: "Счета", headers: []string{"Id", "Name", "Type", "Access", "Status", ""}}
	for _, acc := range resp.GetAccounts() {
		mark := ""
		if acc.GetId() == a.acc {
			mark = c(bgreen, "<== active")
		}
		access := strings.TrimPrefix(acc.GetAccessLevel().String(), "ACCOUNT_ACCESS_LEVEL_")
		if access == "FULL_ACCESS" {
			access = c(green, access)
		} else {
			access = c(yellow, access)
		}
		t.add(acc.GetId(), acc.GetName(), strings.TrimPrefix(acc.GetType().String(), "ACCOUNT_TYPE_"),
			access, strings.TrimPrefix(acc.GetStatus().String(), "ACCOUNT_STATUS_"), mark)
	}
	t.print()

	if a.acc != "" {
		return a.balance()
	}
	return nil
}

func (a *app) balance() error {
	if err := a.needAccount(); err != nil {
		return err
	}
	done := spin("Запрос позиций " + a.acc)
	pos, err := a.client.NewOperationsServiceClient().GetPositions(a.acc)
	done(err)
	if err != nil {
		return err
	}
	t := &table{title: "Баланс " + a.acc, headers: []string{"Asset", "Amount", "Blocked"}, right: map[int]bool{1: true, 2: true}}
	blocked := map[string]float64{}
	for _, m := range pos.GetBlocked() {
		blocked[m.GetCurrency()] = m.ToFloat()
	}
	for _, m := range pos.GetMoney() {
		t.add(strings.ToUpper(m.GetCurrency()), fmt.Sprintf("%.2f", m.ToFloat()), fmt.Sprintf("%.2f", blocked[m.GetCurrency()]))
	}
	for _, s := range pos.GetSecurities() {
		name := s.GetInstrumentUid()
		if a.target != nil && a.target.uid == name {
			name = a.target.ticker
		}
		t.add(name, fmt.Sprint(s.GetBalance()), fmt.Sprint(s.GetBlocked()))
	}
	t.print()
	return nil
}

// ---------- инструмент ----------

type instrument struct {
	uid, figi, ticker, class, name, currency, kind string
	lot                                            int64
	step                                           *pb.Quotation
	apiTrade                                       bool
}

// resolve ищет инструмент по тикеру (можно TICKER@CLASS), предпочитая доступные через API
func (a *app) resolve(q string) (*instrument, error) {
	q = strings.ToUpper(q)
	if a.target != nil && (q == "" || q == a.target.ticker || q == a.target.ticker+"@"+a.target.class) {
		return a.target, nil
	}
	if q == "" {
		return nil, errors.New("инструмент не выбран — " + c(bold, "use <TICKER>"))
	}
	ticker, class, _ := strings.Cut(q, "@")
	is := a.client.NewInstrumentsServiceClient()
	done := spin("Поиск инструмента " + q)
	found, err := is.FindInstrument(ticker)
	if err != nil {
		done(err)
		return nil, err
	}
	var cands []*pb.InstrumentShort
	for _, it := range found.GetInstruments() {
		if !strings.EqualFold(it.GetTicker(), ticker) {
			continue
		}
		if class != "" && !strings.EqualFold(it.GetClassCode(), class) {
			continue
		}
		cands = append(cands, it)
	}
	if len(cands) == 0 {
		err = fmt.Errorf("не найден")
		done(err)
		return nil, err
	}
	sort.SliceStable(cands, func(i, j int) bool { return score(cands[i]) > score(cands[j]) })
	resp, err := is.InstrumentByUid(cands[0].GetUid())
	done(err)
	if err != nil {
		return nil, err
	}
	if len(cands) > 1 {
		var alt []string
		for _, x := range cands[1:] {
			alt = append(alt, x.GetTicker()+"@"+x.GetClassCode())
		}
		warn("Неоднозначно, выбран %s; другие: %s", c(bold, cands[0].GetTicker()+"@"+cands[0].GetClassCode()), strings.Join(alt, ", "))
	}
	in := resp.GetInstrument()
	return &instrument{
		uid: in.GetUid(), figi: in.GetFigi(), ticker: in.GetTicker(), class: in.GetClassCode(),
		name: in.GetName(), currency: strings.ToUpper(in.GetCurrency()), kind: in.GetInstrumentType(),
		lot: int64(in.GetLot()), step: in.GetMinPriceIncrement(), apiTrade: in.GetApiTradeAvailableFlag(),
	}, nil
}

func score(it *pb.InstrumentShort) int {
	s := 0
	if it.GetApiTradeAvailableFlag() {
		s += 10
	}
	switch it.GetClassCode() {
	case "TQBR", "TQTF", "TQCB", "TQOB", "SPBFUT":
		s += 5
	}
	return s
}

func (a *app) use(q string) error {
	in, err := a.resolve(q)
	if err != nil {
		return err
	}
	a.target = in
	good("TARGET => %s", c(bold, in.ticker+"@"+in.class))
	a.instrumentInfo()
	return nil
}

func (a *app) instrumentInfo() {
	in := a.target
	if in == nil {
		bad("инструмент не выбран — %s", c(bold, "use <TICKER>"))
		return
	}
	api := c(green, "yes")
	if !in.apiTrade {
		api = c(red, "no")
	}
	kv("Инструмент",
		[2]string{"Ticker", in.ticker},
		[2]string{"Name", in.name},
		[2]string{"Class", in.class},
		[2]string{"Type", in.kind},
		[2]string{"Lot", fmt.Sprintf("%d шт.", in.lot)},
		[2]string{"Tick", fp(in.step.ToFloat()) + " " + in.currency},
		[2]string{"API trade", api},
		[2]string{"UID", c(dim, in.uid)},
		[2]string{"FIGI", c(dim, in.figi)},
	)
}

// ---------- стакан ----------

type snapshot struct {
	Bid, Ask, Mid, Last float64
	Status              string
	MarketOrders        bool
	bids, asks          []*pb.Order
}

func (a *app) snapshot(in *instrument, depth int32) (*snapshot, error) {
	md := a.client.NewMarketDataServiceClient()
	st, err := md.GetTradingStatus(in.uid)
	if err != nil {
		return nil, fmt.Errorf("GetTradingStatus: %w", err)
	}
	ob, err := md.GetOrderBook(in.uid, depth)
	if err != nil {
		return nil, fmt.Errorf("GetOrderBook: %w", err)
	}
	s := &snapshot{
		Last:         ob.GetLastPrice().ToFloat(),
		Status:       strings.TrimPrefix(st.GetTradingStatus().String(), "SECURITY_TRADING_STATUS_"),
		MarketOrders: st.GetMarketOrderAvailableFlag(),
		bids:         ob.GetBids(),
		asks:         ob.GetAsks(),
	}
	if len(s.bids) > 0 {
		s.Bid = s.bids[0].GetPrice().ToFloat()
	}
	if len(s.asks) > 0 {
		s.Ask = s.asks[0].GetPrice().ToFloat()
	}
	if s.Bid > 0 && s.Ask > 0 {
		s.Mid = (s.Bid + s.Ask) / 2
	}
	return s, nil
}

// renderBook возвращает строки стакана: аски сверху (красные), биды снизу (зелёные)
func renderBook(s *snapshot, in *instrument) []string {
	var max int64
	for _, o := range append(append([]*pb.Order{}, s.bids...), s.asks...) {
		if o.GetQuantity() > max {
			max = o.GetQuantity()
		}
	}
	var out []string
	for i := len(s.asks) - 1; i >= 0; i-- {
		o := s.asks[i]
		out = append(out, fmt.Sprintf("   %s %s %s  %s", c(red, "ask"), padL(c(red, fp(o.GetPrice().ToFloat())), 12),
			padL(fmt.Sprint(o.GetQuantity()), 8), bar(o.GetQuantity(), max, 24, red)))
	}
	sp := ""
	if s.Mid > 0 {
		sp = fmt.Sprintf(" spread %s (%.0f tick, %.3f%%) ", fp(s.Ask-s.Bid), (s.Ask-s.Bid)/in.step.ToFloat(), (s.Ask-s.Bid)/s.Mid*100)
	}
	out = append(out, c(dim, "   "+strings.Repeat("─", 6))+c(byellow, sp)+c(dim, strings.Repeat("─", 6)))
	for _, o := range s.bids {
		out = append(out, fmt.Sprintf("   %s %s %s  %s", c(green, "bid"), padL(c(green, fp(o.GetPrice().ToFloat())), 12),
			padL(fmt.Sprint(o.GetQuantity()), 8), bar(o.GetQuantity(), max, 24, green)))
	}
	return out
}

func statusColor(s string) string {
	if s == "NORMAL_TRADING" {
		return c(bgreen, s)
	}
	return c(byellow, s)
}

// ---------- quote: полная цена круга ----------

func (a *app) quote(q string) error {
	in, err := a.resolve(q)
	if err != nil {
		return err
	}
	done := spin("Снимок стакана " + in.ticker)
	s, err := a.snapshot(in, 5)
	done(err)
	if err != nil {
		return err
	}
	fmt.Println()
	for _, l := range renderBook(s, in) {
		fmt.Println(l)
	}
	fmt.Println()
	info("Статус торгов: %s   рыночные заявки: %v   последняя сделка: %s", statusColor(s.Status), s.MarketOrders, c(bold, fp(s.Last)))
	if s.Mid == 0 {
		warn("Стакан пуст — торгов сейчас нет, спред не посчитать.")
		return nil
	}
	lot := float64(in.lot)
	spread := s.Ask - s.Bid

	t := &table{title: "Цена круга: купить 1 лот и сразу продать", headers: []string{"Item", in.currency, "%", "Note"}, right: map[int]bool{1: true, 2: true}}
	t.add("Лот по ask", fmt.Sprintf("%.2f", s.Ask*lot), "", fmt.Sprintf("%d шт. x %s", in.lot, fp(s.Ask)))
	t.add("Лот по bid", fmt.Sprintf("%.2f", s.Bid*lot), "", fmt.Sprintf("%d шт. x %s", in.lot, fp(s.Bid)))
	t.add("Спред", fmt.Sprintf("%.2f", spread*lot), fmt.Sprintf("%.3f", spread/s.Mid*100), fmt.Sprintf("%.0f шаг. цены", spread/in.step.ToFloat()))

	var fees float64
	if a.acc == "" {
		warn("ACCOUNT не задан — комиссию брокера не оценить")
	} else {
		ords := a.client.NewOrdersServiceClient()
		for _, side := range []struct {
			dir   pb.OrderDirection
			price float64
			name  string
		}{
			{pb.OrderDirection_ORDER_DIRECTION_BUY, s.Ask, "Комиссия покупки"},
			{pb.OrderDirection_ORDER_DIRECTION_SELL, s.Bid, "Комиссия продажи"},
		} {
			done := spin("GetOrderPrice: " + strings.ToLower(side.name))
			p, err := ords.GetOrderPrice(a.acc, in.uid, investgo.FloatToQuotation(side.price, in.step), side.dir, 1)
			done(err)
			if err != nil {
				continue
			}
			amt, fee := p.GetInitialOrderAmount().ToFloat(), p.GetExecutedCommission().ToFloat()
			fees += fee
			t.add(side.name, fmt.Sprintf("%.2f", fee), fmt.Sprintf("%.3f", fee/math.Max(amt, 1e-9)*100),
				fmt.Sprintf("сделка %.2f, сервис %.2f", p.GetDealCommission().ToFloat(), p.GetServiceCommission().ToFloat()))
		}
	}
	total := spread*lot + fees
	t.add(c(bold, "ИТОГО круг"), c(bred, fmt.Sprintf("%.2f", total)), c(bred, fmt.Sprintf("%.3f", total/(s.Mid*lot)*100)), "спред + комиссии")
	t.print()

	info("Безубыток: цена должна пройти %s %s (%.1f шаг.) в вашу сторону",
		c(bold, fmt.Sprintf("%.4f", total/lot)), in.currency, total/lot/in.step.ToFloat())
	if a.acc != "" {
		if ml, err := a.client.NewOrdersServiceClient().GetMaxLots(a.acc, in.uid, nil); err == nil {
			info("Доступно: купить до %d лот. по рынку, продать до %d лот.",
				ml.GetBuyLimits().GetBuyMaxMarketLots(), ml.GetSellLimits().GetSellMaxLots())
		}
	}
	return nil
}

// ---------- impact: проход по стакану крупной рыночной заявкой ----------

func (a *app) impact(q string, amount float64) error {
	in, err := a.resolve(q)
	if err != nil {
		return err
	}
	done := spin("Стакан " + in.ticker + " на глубину 50")
	s, err := a.snapshot(in, 50)
	done(err)
	if err != nil {
		return err
	}
	if s.Mid == 0 {
		return errors.New("стакан пуст — торгов нет")
	}
	lot := float64(in.lot)
	feeRate := 0.003
	if a.acc != "" {
		// ставку комиссии берём у брокера на 1 лот
		if p, err := a.client.NewOrdersServiceClient().GetOrderPrice(a.acc, in.uid,
			investgo.FloatToQuotation(s.Ask, in.step), pb.OrderDirection_ORDER_DIRECTION_BUY, 1); err == nil {
			if amt := p.GetInitialOrderAmount().ToFloat(); amt > 0 {
				feeRate = p.GetExecutedCommission().ToFloat() / amt
			}
		}
	}

	for _, side := range []struct {
		name   string
		levels []*pb.Order
		best   float64
		buy    bool
	}{{"ПОКУПКА", s.asks, s.Ask, true}, {"ПРОДАЖА", s.bids, s.Bid, false}} {
		t := &table{title: fmt.Sprintf("%s на %.0f %s по рынку", side.name, amount, in.currency),
			headers: []string{"Level", "Price", "Lots", "Sum", "Cum sum"}, right: map[int]bool{1: true, 2: true, 3: true, 4: true}}
		left := amount
		var lots int64
		var spent float64
		for i, o := range side.levels {
			p := o.GetPrice().ToFloat()
			lotCost := p * lot
			take := min(int64(left/lotCost), o.GetQuantity())
			if take <= 0 {
				break
			}
			lots += take
			spent += float64(take) * lotCost
			left -= float64(take) * lotCost
			t.add(fmt.Sprint(i+1), fp(p), fmt.Sprintf("%d / %d", take, o.GetQuantity()),
				fmt.Sprintf("%.0f", float64(take)*lotCost), fmt.Sprintf("%.0f", spent))
			if take < o.GetQuantity() {
				break
			}
		}
		if lots == 0 {
			warn("%s: суммы не хватает даже на 1 лот", side.name)
			continue
		}
		t.print()
		avg := spent / (float64(lots) * lot)
		slipBest := (avg - side.best) / side.best * 100
		slipMid := (avg - s.Mid) / s.Mid * 100
		if !side.buy {
			slipBest, slipMid = -slipBest, -slipMid
		}
		fee := spent * feeRate
		var depth float64
		for _, o := range side.levels {
			depth += o.GetPrice().ToFloat() * lot * float64(o.GetQuantity())
		}
		pairs := [][2]string{
			{"Filled", fmt.Sprintf("%d лот. = %d шт. на %.2f %s", lots, lots*in.lot, spent, in.currency)},
			{"Avg price", c(bold, fp(avg))},
			{"vs best", fmt.Sprintf("%s → %+.3f%%", fp(side.best), slipBest)},
			{"vs mid", fmt.Sprintf("%s → %+.3f%%  (%.2f %s)", fp(s.Mid), slipMid, slipMid/100*spent, in.currency)},
			{"Commission", fmt.Sprintf("%.2f %s (%.3f%%)", fee, in.currency, feeRate*100)},
			{"Total cost", c(bred, fmt.Sprintf("%.3f%%  (%.2f %s)", slipMid+feeRate*100, slipMid/100*spent+fee, in.currency))},
			{"Book depth", fmt.Sprintf("%.0f %s в %d уровнях", depth, in.currency, len(side.levels))},
		}
		if left >= side.best*lot && depth <= spent+1 {
			pairs = append(pairs, [2]string{"!!!", c(bred, "стакана не хватило — остаток исполнится ещё хуже")})
		}
		kv(side.name+": итог", pairs...)
	}
	warn("Это снимок на %s; стакан меняется каждую секунду, крупные заявки видны не все.", time.Now().Format("15:04:05"))
	return nil
}

// ---------- watch: живой стакан ----------

func (a *app) watch(q string, stop <-chan struct{}) error {
	in, err := a.resolve(q)
	if err != nil {
		return err
	}
	info("Живой стакан %s, обновление 1 с. %s", c(bold, in.ticker), c(dim, "[Enter — выход]"))
	prev := 0
	var lastMid float64
	for {
		s, err := a.snapshot(in, 10)
		if err != nil {
			return err
		}
		lines := renderBook(s, in)
		arrow := c(dim, "=")
		if lastMid > 0 && s.Mid > lastMid {
			arrow = c(bgreen, "▲")
		} else if lastMid > 0 && s.Mid < lastMid {
			arrow = c(bred, "▼")
		}
		lastMid = s.Mid
		lines = append(lines, fmt.Sprintf("   %s mid %s  last %s  %s  %s", arrow, c(bold, fp(s.Mid)), fp(s.Last),
			statusColor(s.Status), c(dim, time.Now().Format("15:04:05"))))
		if prev > 0 {
			fmt.Printf("\x1b[%dA", prev)
		}
		for _, l := range lines {
			fmt.Print("\x1b[2K" + l + "\n")
		}
		prev = len(lines)
		select {
		case <-stop:
			return nil
		case <-time.After(time.Second):
		}
	}
}

// ---------- сделка ----------

type journalEntry struct {
	Time       time.Time `json:"time"`
	Account    string    `json:"account"`
	Ticker     string    `json:"ticker"`
	UID        string    `json:"uid"`
	Side       string    `json:"side"`
	Lots       int64     `json:"lots"`
	Lot        int64     `json:"lot"`
	OrderID    string    `json:"order_id"`
	Bid        float64   `json:"bid"`
	Ask        float64   `json:"ask"`
	Mid        float64   `json:"mid"`
	Status     string    `json:"status"`
	ExecPrice  float64   `json:"exec_price"`
	Commission float64   `json:"commission"`
	Total      float64   `json:"total"`
	LatencyMs  int64     `json:"latency_ms"`
}

func (a *app) trade(q string, buy bool) error {
	if err := a.needAccount(); err != nil {
		return err
	}
	in, err := a.resolve(q)
	if err != nil {
		return err
	}
	lots := a.lots
	side := map[bool]string{true: "BUY", false: "SELL"}[buy]
	sideC := map[bool]string{true: c(bgreen, "BUY"), false: c(bred, "SELL")}[buy]

	done := spin("Проверка рынка " + in.ticker)
	s, err := a.snapshot(in, 1)
	done(err)
	if err != nil {
		return err
	}
	if s.Status != "NORMAL_TRADING" || !s.MarketOrders || s.Mid == 0 {
		return fmt.Errorf("%s не торгуется в нормальном режиме (%s) — заявку не ставлю", in.ticker, s.Status)
	}
	if !buy {
		pos, err := a.client.NewOperationsServiceClient().GetPositions(a.acc)
		if err != nil {
			return err
		}
		var have int64
		for _, p := range pos.GetSecurities() {
			if p.GetInstrumentUid() == in.uid {
				have = p.GetBalance()
			}
		}
		if have < lots*in.lot {
			return fmt.Errorf("на счёте %d шт. %s, а продать нужно %d — в шорт не идём", have, in.ticker, lots*in.lot)
		}
	}

	ref := map[bool]float64{true: s.Ask, false: s.Bid}[buy]
	kv("Заявка",
		[2]string{"Mode", liveBadge(a.live)},
		[2]string{"Account", a.acc},
		[2]string{"Side", sideC},
		[2]string{"Target", in.ticker + "@" + in.class},
		[2]string{"Qty", fmt.Sprintf("%d лот. = %d шт.", lots, lots*in.lot)},
		[2]string{"Type", "MARKET"},
		[2]string{"Bid / Ask", fp(s.Bid) + " / " + fp(s.Ask)},
		[2]string{"≈ Amount", fmt.Sprintf("%.2f %s", ref*float64(lots*in.lot), in.currency)},
	)
	if !a.confirm(fmt.Sprintf("Выставить %s %d лот. %s по рынку?", side, lots, in.ticker)) {
		warn("Отменено. Заявка не выставлена.")
		return nil
	}

	ords := a.client.NewOrdersServiceClient()
	req := &investgo.PostOrderRequestShort{
		InstrumentId: in.uid, Quantity: lots, AccountId: a.acc,
		OrderType: pb.OrderType_ORDER_TYPE_MARKET, OrderId: investgo.CreateUid(),
	}
	done = spin(fmt.Sprintf("Отправка %s %s", side, in.ticker))
	t0 := time.Now()
	var resp *investgo.PostOrderResponse
	if buy {
		resp, err = ords.Buy(req)
	} else {
		resp, err = ords.Sell(req)
	}
	lat := time.Since(t0)
	done(err)
	if err != nil {
		return err
	}
	good("Заявка %s: %s, исполнено %d/%d", c(dim, resp.GetOrderId()),
		c(bold, strings.TrimPrefix(resp.GetExecutionReportStatus().String(), "EXECUTION_REPORT_STATUS_")),
		resp.GetLotsExecuted(), resp.GetLotsRequested())

	// комиссия и итоговая цена часто доезжают не сразу — опрашиваем состояние
	done = spin("Ожидание исполнения и комиссии")
	var st *investgo.GetOrderStateResponse
	for i := 0; i < 20; i++ {
		st, err = ords.GetOrderState(a.acc, resp.GetOrderId(), pb.PriceType_PRICE_TYPE_CURRENCY, nil)
		if err == nil && st.GetExecutionReportStatus() == pb.OrderExecutionReportStatus_EXECUTION_REPORT_STATUS_FILL &&
			st.GetExecutedCommission().ToFloat() != 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	done(err)
	if err != nil {
		return err
	}

	shares := float64(st.GetLotsExecuted() * in.lot)
	var execSum float64
	tf := &table{title: "Исполнение", headers: []string{"Time", "Qty", "Price", "Trade"}, right: map[int]bool{1: true, 2: true}}
	for _, stg := range st.GetStages() {
		// price в stages — цена за 1 инструмент, quantity — в штуках
		tf.add(stg.GetExecutionTime().AsTime().Local().Format("15:04:05.000"), fmt.Sprint(stg.GetQuantity()),
			fp(stg.GetPrice().ToFloat()), c(dim, stg.GetTradeId()))
		execSum += stg.GetPrice().ToFloat() * float64(stg.GetQuantity())
	}
	if len(tf.rows) > 0 {
		tf.print()
	}
	execPrice := st.GetExecutedOrderPrice().ToFloat() / math.Max(shares, 1)
	if execSum > 0 && shares > 0 {
		execPrice = execSum / shares
	}
	fee := st.GetExecutedCommission().ToFloat()
	gross := execPrice * shares
	var slip float64 // во что обошлось пересечение спреда относительно середины
	if buy {
		slip = (execPrice - s.Mid) * shares
	} else {
		slip = (s.Mid - execPrice) * shares
	}
	kv("Разбор сделки",
		[2]string{"Avg price", c(bold, fp(execPrice))},
		[2]string{"Mid before", fp(s.Mid)},
		[2]string{"Latency", fmt.Sprintf("%d ms", lat.Milliseconds())},
		[2]string{"Gross", fmt.Sprintf("%.2f %s", gross, in.currency)},
		[2]string{"Commission", fmt.Sprintf("%.2f %s (%.3f%%)", fee, in.currency, fee/math.Max(gross, 1e-9)*100)},
		[2]string{"Spread cost", fmt.Sprintf("%.2f %s (от середины)", slip, in.currency)},
		[2]string{"Total", c(bold, fmt.Sprintf("%.2f %s", st.GetTotalOrderAmount().ToFloat(), in.currency))},
	)
	return appendJournal(journalEntry{
		Time: t0, Account: a.acc, Ticker: in.ticker, UID: in.uid, Side: side, Lots: st.GetLotsExecuted(), Lot: in.lot,
		OrderID: resp.GetOrderId(), Bid: s.Bid, Ask: s.Ask, Mid: s.Mid,
		Status: st.GetExecutionReportStatus().String(), ExecPrice: execPrice, Commission: fee,
		Total: st.GetTotalOrderAmount().ToFloat(), LatencyMs: lat.Milliseconds(),
	})
}

func appendJournal(e journalEntry) error {
	f, err := os.OpenFile(journalFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(e)
}

func readJournal() []journalEntry {
	data, err := os.ReadFile(journalFile)
	if err != nil {
		return nil
	}
	var out []journalEntry
	for _, line := range strings.Split(string(data), "\n") {
		var e journalEntry
		if json.Unmarshal([]byte(line), &e) == nil && e.OrderID != "" {
			out = append(out, e)
		}
	}
	return out
}

// ---------- отчёт ----------

func (a *app) report(q, since string) error {
	if err := a.needAccount(); err != nil {
		return err
	}
	in, err := a.resolve(q)
	if err != nil {
		return err
	}
	if since == "" {
		since = time.Now().Format("2006-01-02")
	}
	from, err := time.ParseInLocation("2006-01-02", since, time.Local)
	if err != nil {
		return fmt.Errorf("дата: %w", err)
	}

	ops := a.client.NewOperationsServiceClient()
	var items []*pb.OperationItem
	cursor := ""
	done := spin("Загрузка операций " + in.ticker + " с " + since)
	for {
		r, err := ops.GetOperationsByCursor(&investgo.GetOperationsByCursorRequest{
			AccountId: a.acc, InstrumentId: in.uid, From: from, To: time.Now().Add(time.Minute),
			Cursor: cursor, Limit: 1000, State: pb.OperationState_OPERATION_STATE_EXECUTED,
		})
		if err != nil {
			done(err)
			return err
		}
		items = append(items, r.GetItems()...)
		if !r.GetHasNext() {
			break
		}
		cursor = r.GetNextCursor()
	}
	done(nil)
	sort.Slice(items, func(i, j int) bool { return items[i].GetDate().AsTime().Before(items[j].GetDate().AsTime()) })

	t := &table{title: "Операции " + in.ticker, headers: []string{"Date", "Type", "Qty", "Price", "Payment", "Fee"},
		right: map[int]bool{2: true, 3: true, 4: true, 5: true}}
	var buys, sells, feeOps, feeFields float64
	var qBuy, qSell int64
	for _, it := range items {
		pay := it.GetPayment().ToFloat()
		typ := strings.TrimPrefix(it.GetType().String(), "OPERATION_TYPE_")
		switch it.GetType() {
		case pb.OperationType_OPERATION_TYPE_BUY:
			buys += pay
			qBuy += it.GetQuantityDone()
			feeFields += math.Abs(it.GetCommission().ToFloat())
			typ = c(green, typ)
		case pb.OperationType_OPERATION_TYPE_SELL:
			sells += pay
			qSell += it.GetQuantityDone()
			feeFields += math.Abs(it.GetCommission().ToFloat())
			typ = c(red, typ)
		case pb.OperationType_OPERATION_TYPE_BROKER_FEE:
			feeOps += math.Abs(pay)
			typ = c(yellow, typ)
		}
		t.add(it.GetDate().AsTime().Local().Format("02.01 15:04:05"), typ, fmt.Sprint(it.GetQuantityDone()),
			fp(it.GetPrice().ToFloat()), fmt.Sprintf("%.2f", pay), fmt.Sprintf("%.2f", it.GetCommission().ToFloat()))
	}
	if len(items) == 0 {
		warn("Операций нет")
		return nil
	}
	t.print()

	fees := feeOps
	if fees == 0 {
		fees = feeFields
	}
	if qBuy != qSell {
		warn("Позиция не закрыта (разница %d шт.) — итог неполный", qBuy-qSell)
	}
	pairs := [][2]string{
		{"Bought", fmt.Sprintf("%d шт. на %.2f %s", qBuy, -buys, in.currency)},
		{"Sold", fmt.Sprintf("%d шт. на %.2f %s", qSell, sells, in.currency)},
		{"Price P&L", pnl(sells+buys, in.currency)},
		{"Broker fees", pnl(-fees, in.currency) + c(dim, fmt.Sprintf("  (операции %.2f / поля сделок %.2f)", feeOps, feeFields))},
	}

	var spreadCost float64
	var n int
	for _, e := range readJournal() {
		if e.UID != in.uid || e.Account != a.acc || e.Time.Before(from) {
			continue
		}
		shares := float64(e.Lots * e.Lot)
		if e.Side == "BUY" {
			spreadCost += (e.ExecPrice - e.Mid) * shares
		} else {
			spreadCost += (e.Mid - e.ExecPrice) * shares
		}
		n++
	}
	if n > 0 {
		pairs = append(pairs,
			[2]string{"  incl. spread", pnl(-spreadCost, in.currency) + c(dim, fmt.Sprintf("  (журнал, %d заявок)", n))},
			[2]string{"  incl. market", pnl(sells+buys+spreadCost, in.currency) + c(dim, "  (движение цены)")},
		)
	}
	pairs = append(pairs, [2]string{c(bold, "NET"), pnl(sells+buys-fees, in.currency)})
	kv("Итог", pairs...)
	return nil
}

// fp печатает цену без лишних нулей
func fp(v float64) string {
	s := fmt.Sprintf("%.6f", v)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}
