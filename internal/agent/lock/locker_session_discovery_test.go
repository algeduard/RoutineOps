package lock

import "testing"

func TestLoginctlSessionIDs(t *testing.T) {
	out := "" +
		"2 1000 alice seat0 tty2\n" +
		"c1 116 gdm  seat0 tty1\n" +
		"7 1001 bob\n" + // ssh-сессия без места — id всё равно берём, отфильтрует уже activeX11FromProps
		"мусор без uid\n"
	ids := loginctlSessionIDs(out)
	want := []string{"2", "c1", "7"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, ожидали %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids[%d] = %q, ожидали %q", i, ids[i], want[i])
		}
	}
}

func TestParseLoginctlProps(t *testing.T) {
	out := "Active=yes\nType=x11\nUser=1000\nName=alice\nDisplay=:0\n"
	p := parseLoginctlProps(out)
	for k, want := range map[string]string{
		"Active": "yes", "Type": "x11", "User": "1000", "Name": "alice", "Display": ":0",
	} {
		if p[k] != want {
			t.Errorf("props[%q] = %q, ожидали %q", k, p[k], want)
		}
	}
}

func TestActiveX11FromProps(t *testing.T) {
	base := func() map[string]string {
		return map[string]string{"Active": "yes", "Type": "x11", "User": "1000", "Name": "alice", "Display": ":0"}
	}

	t.Run("годная X11-сессия", func(t *testing.T) {
		uid, display, name, ok := activeX11FromProps(base())
		if !ok || uid != 1000 || display != ":0" || name != "alice" {
			t.Fatalf("got uid=%d display=%q name=%q ok=%v", uid, display, name, ok)
		}
	})

	reject := map[string]func(map[string]string){
		"неактивная":           func(m map[string]string) { m["Active"] = "no" },
		"wayland":              func(m map[string]string) { m["Type"] = "wayland" },
		"greeter под UID<1000": func(m map[string]string) { m["User"] = "116" },
		"пустой Display":       func(m map[string]string) { m["Display"] = "" },
		"нечисловой User":      func(m map[string]string) { m["User"] = "root" },
	}
	for name, mangle := range reject {
		t.Run(name, func(t *testing.T) {
			m := base()
			mangle(m)
			if _, _, _, ok := activeX11FromProps(m); ok {
				t.Fatalf("ожидали отказ для случая %q", name)
			}
		})
	}
}
