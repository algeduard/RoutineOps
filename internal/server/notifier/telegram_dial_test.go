package notifier

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestDialParallel_PicksLiveAmongDead: среди «мёртвых» (не маршрутизируемых) адресов
// dialParallel находит и возвращает живой listener — эмуляция частично
// заблокированного api.telegram.org, где открыт лишь один из IP.
func TestDialParallel_PicksLiveAmongDead(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			c.Close()
		}
	}()

	live := ln.Addr().String()
	// 192.0.2.0/24 (TEST-NET-1, RFC 5737) не маршрутизируется → dial уходит в
	// таймаут/ошибку, как заблокированный IP Telegram.
	dead1, dead2 := "192.0.2.1:443", "192.0.2.2:443"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := dialParallel(ctx, "tcp", []string{dead1, dead2, live})
	if err != nil {
		t.Fatalf("ждали соединение с живым target, получили ошибку: %v", err)
	}
	defer conn.Close()
	if conn.RemoteAddr().String() != live {
		t.Fatalf("подключились к %s, а живой был %s", conn.RemoteAddr(), live)
	}
}

// TestDialParallel_AllDeadReturnsError: если живых нет — возвращается ошибка, а не
// вечное зависание.
func TestDialParallel_AllDeadReturnsError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := dialParallel(ctx, "tcp", []string{"192.0.2.1:443", "192.0.2.2:443"})
	if err == nil {
		conn.Close()
		t.Fatal("ждали ошибку при всех мёртвых target, получили соединение")
	}
}
