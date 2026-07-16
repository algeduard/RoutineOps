package notifier

import (
	"context"
	"net"
	"net/http"
	"time"
)

// telegramHTTPClient — HTTP-клиент для Bot API, устойчивый к ЧАСТИЧНОЙ блокировке
// api.telegram.org (актуально для РФ и подобных сетей). Проблема: api.telegram.org
// резолвится в НЕСКОЛЬКО IP из разных дата-центров Telegram, и часть из них может
// быть заблокирована (SYN уходит в чёрную дыру). Round-robin DNS то и дело
// подсовывает именно заблокированный IP → getUpdates/sendMessage отваливаются по
// «i/o timeout», хотя ДРУГИЕ IP того же api.telegram.org открыты. dialFastest
// дозванивается по всем IP сразу и берёт первое живое соединение — бот находит
// рабочий ДЦ за один RTT независимо от порядка DNS, и телеграм-уведомления работают
// у нового пользователя «из коробки», без ручной настройки.
//
// Полная блокировка (закрыты ВСЕ IP) лечится только прокси — поэтому базовый
// http.ProxyFromEnvironment сохранён (клонируем DefaultTransport): HTTPS_PROXY
// по-прежнему уважается как аварийный обходной путь.
func telegramHTTPClient(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = dialFastest
	return &http.Client{Timeout: timeout, Transport: tr}
}

// dialFastest резолвит host из addr и дозванивается по всем его IP параллельно,
// возвращая первое успешное соединение. Для хостов с одним IP (или при ошибке
// резолва) вырождается в обычный dial — без накладных расходов.
func dialFastest(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) <= 1 {
		var d net.Dialer
		return d.DialContext(ctx, network, addr) // нечего распараллеливать
	}
	targets := make([]string, len(ips))
	for i, ip := range ips {
		targets[i] = net.JoinHostPort(ip.IP.String(), port)
	}
	return dialParallel(ctx, network, targets)
}

// dialParallel дозванивается по всем targets одновременно и возвращает первое живое
// соединение; остальные дозвоны отменяются, их поздние успехи закрываются в фоне
// (иначе утекли бы открытые сокеты). Возвращает первую ошибку, если живых нет.
func dialParallel(ctx context.Context, network string, targets []string) (net.Conn, error) {
	ctx, cancel := context.WithCancel(ctx)
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, len(targets)) // буфер = никто не блокируется на отправке
	var d net.Dialer
	for _, t := range targets {
		go func(target string) {
			c, e := d.DialContext(ctx, network, target)
			ch <- result{c, e}
		}(t)
	}
	var firstErr error
	for i := 0; i < len(targets); i++ {
		r := <-ch
		if r.err == nil {
			cancel()
			if rest := len(targets) - i - 1; rest > 0 {
				go func() { // добить проигравших: отменённые дозвоны + закрыть поздних победителей
					for j := 0; j < rest; j++ {
						if rr := <-ch; rr.conn != nil {
							rr.conn.Close()
						}
					}
				}()
			}
			return r.conn, nil
		}
		if firstErr == nil {
			firstErr = r.err
		}
	}
	cancel()
	return nil, firstErr
}
