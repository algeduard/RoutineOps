//go:build windows

package remotedesktop

import (
	"unsafe"

	pb "github.com/Floodww/RoutineOps/proto"
	"github.com/lxn/win"
)

// winInjector применяет события ввода через SendInput. Координаты мыши приходят
// нормализованными 0..1 и переводятся в абсолютные по ВСЕМУ виртуальному десктопу
// (MOUSEEVENTF_ABSOLUTE|VIRTUALDESK, диапазон 0..65535).
type winInjector struct{ w, h int }

func newInjector(w, h int) injector { return &winInjector{w: w, h: h} }

func (in *winInjector) Inject(ev *pb.RDInputEvent) {
	switch ev.GetType() {
	case pb.RDInputType_RD_INPUT_TYPE_MOUSE_MOVE:
		in.mouse(ev, win.MOUSEEVENTF_MOVE)
	case pb.RDInputType_RD_INPUT_TYPE_MOUSE_DOWN:
		in.mouse(ev, win.MOUSEEVENTF_MOVE|mouseButtonFlag(ev.GetButton(), true))
	case pb.RDInputType_RD_INPUT_TYPE_MOUSE_UP:
		in.mouse(ev, win.MOUSEEVENTF_MOVE|mouseButtonFlag(ev.GetButton(), false))
	case pb.RDInputType_RD_INPUT_TYPE_WHEEL:
		in.wheel(ev)
	case pb.RDInputType_RD_INPUT_TYPE_KEY:
		in.key(ev)
	}
}

func (in *winInjector) mouse(ev *pb.RDInputEvent, extraFlags uint32) {
	sendMouse(normTo64k(ev.GetX()), normTo64k(ev.GetY()), 0,
		win.MOUSEEVENTF_ABSOLUTE|win.MOUSEEVENTF_VIRTUALDESK|extraFlags)
}

func (in *winInjector) wheel(ev *pb.RDInputEvent) {
	sendMouse(normTo64k(ev.GetX()), normTo64k(ev.GetY()), uint32(ev.GetWheelDelta()),
		win.MOUSEEVENTF_ABSOLUTE|win.MOUSEEVENTF_VIRTUALDESK|win.MOUSEEVENTF_WHEEL)
}

func (in *winInjector) key(ev *pb.RDInputEvent) {
	var flags uint32
	if !ev.GetKeyDown() {
		flags |= win.KEYEVENTF_KEYUP
	}
	ki := win.KEYBD_INPUT{
		Type: win.INPUT_KEYBOARD,
		Ki: win.KEYBDINPUT{
			WVk:     uint16(ev.GetKeyCode()),
			DwFlags: flags,
		},
	}
	win.SendInput(1, unsafe.Pointer(&ki), int32(unsafe.Sizeof(ki)))
}

func sendMouse(dx, dy int32, mouseData, flags uint32) {
	mi := win.MOUSE_INPUT{
		Type: win.INPUT_MOUSE,
		Mi: win.MOUSEINPUT{
			Dx:        dx,
			Dy:        dy,
			MouseData: mouseData,
			DwFlags:   flags,
		},
	}
	win.SendInput(1, unsafe.Pointer(&mi), int32(unsafe.Sizeof(mi)))
}

func mouseButtonFlag(button int32, down bool) uint32 {
	switch button {
	case 1: // правая
		if down {
			return win.MOUSEEVENTF_RIGHTDOWN
		}
		return win.MOUSEEVENTF_RIGHTUP
	case 2: // средняя
		if down {
			return win.MOUSEEVENTF_MIDDLEDOWN
		}
		return win.MOUSEEVENTF_MIDDLEUP
	default: // левая
		if down {
			return win.MOUSEEVENTF_LEFTDOWN
		}
		return win.MOUSEEVENTF_LEFTUP
	}
}

// normTo64k переводит нормализованную координату 0..1 в диапазон SendInput 0..65535.
func normTo64k(v float64) int32 {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return int32(v * 65535.0)
}
