//go:build windows

package telemetry

import (
	"runtime"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
)

// Чтение URL активной вкладки браузера через COM UI Automation (UIA). Заголовок окна
// берётся простым GetWindowText, а вот URL живёт в address bar (Edit-контрол) и
// доступен только через дерево доступности — UIA. Прямого Go-биндинга IUIAutomation в
// go-ole нет, поэтому вызываем COM-методы по vtable вручную (syscall.SyscallN), как это
// принято для не-IDispatch COM-интерфейсов.
//
// Подход (клиент UIA):
//   1) CoCreateInstance(CLSID_CUIAutomation) → IUIAutomation.
//   2) ElementFromHandle(hwnd) → корневой элемент окна.
//   3) CreatePropertyCondition(ControlType == Edit) и FindFirst по поддереву → address
//      bar (омнибокс). У Chrome/Edge/Firefox это первый Edit с валидным значением URL.
//   4) GetCurrentPropertyValue(ValueValue) → строка URL.
//
// Всё best-effort: любая ошибка/паника COM гасится (recover) и даёт "", не ломая сбор
// заголовков/активности. Приватные/инкогнито-окна отсекаются ВЫШЕ (foregroundApp по
// isPrivateBrowsing), сюда для них не заходим.

// GUID клиента UIA и его интерфейса.
var (
	clsidCUIAutomation = ole.NewGUID("{FF48DBA4-60EF-4201-AA87-54103EEF594E}")
	iidIUIAutomation   = ole.NewGUID("{30CBE57D-D9D0-452A-AB13-7AC5AC4825EE}")
)

// UIA-константы (UIAutomationClient.h).
const (
	uiaControlTypePropertyID = 30003
	uiaValueValuePropertyID  = 30045
	uiaEditControlTypeID     = 50004
	treeScopeDescendants     = 4
)

// Слоты vtable (0-based; после IUnknown QueryInterface/AddRef/Release = 0..2).
const (
	// IUIAutomation
	slotElementFromHandle       = 6
	slotCreatePropertyCondition = 23
	// IUIAutomationElement
	slotElementFindFirst               = 5
	slotElementGetCurrentPropertyValue = 10
)

// comCall вызывает метод COM-объекта по номеру слота его vtable. Первый аргумент метода
// — сам this-указатель. Возвращает HRESULT (или иной ret) как uintptr.
func comCall(this unsafe.Pointer, slot int, args ...uintptr) uintptr {
	vtbl := *(**[64]uintptr)(this)
	fn := vtbl[slot]
	full := make([]uintptr, 0, len(args)+1)
	full = append(full, uintptr(this))
	full = append(full, args...)
	ret, _, _ := syscall.SyscallN(fn, full...)
	return ret
}

// comRelease вызывает IUnknown::Release (слот 2).
func comRelease(this unsafe.Pointer) {
	if this != nil {
		comCall(this, 2)
	}
}

// readBrowserURL читает URL активной вкладки из окна hwnd через UIA. "" при любой
// неудаче. Вызывающий гарантирует, что hwnd принадлежит известному браузеру и это НЕ
// приватное/инкогнито-окно.
func readBrowserURL(hwnd uintptr) (result string) {
	// COM-объекты и апартмент завязаны на конкретный поток — пиним горутину, чтобы
	// CoInitialize/вызовы/CoUninitialize шли на одном OS-потоке.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// best-effort: любая паника в unsafe/COM не должна валить сэмплер.
	defer func() {
		if r := recover(); r != nil {
			result = ""
		}
	}()

	// UIA-клиенты рекомендуется инициализировать в MTA. Каждый исход CoInitializeEx
	// значит своё для парности CoUninitialize:
	//   S_OK      — проинициализировали сами → надо Uninit;
	//   S_FALSE   — поток уже в том же режиме, счётчик увеличен → тоже надо Uninit;
	//   CHANGED_MODE — поток уже в ДРУГОМ режиме, НАШ Init не сработал → Uninit НЕ звать,
	//                  но COM инициализирован — работаем (кросс-апартмент через маршалинг).
	needUninit := false
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err == nil {
		needUninit = true
	} else if oleErr, ok := err.(*ole.OleError); ok {
		switch oleErr.Code() {
		case 0x00000001: // S_FALSE
			needUninit = true
		case 0x80010106: // RPC_E_CHANGED_MODE
			needUninit = false
		default:
			return ""
		}
	} else {
		return ""
	}
	if needUninit {
		defer ole.CoUninitialize()
	}

	unk, err := ole.CreateInstance(clsidCUIAutomation, iidIUIAutomation)
	if err != nil || unk == nil {
		return ""
	}
	uia := unsafe.Pointer(unk)
	defer comRelease(uia)

	// ElementFromHandle(hwnd) → корневой элемент окна.
	var rootEl unsafe.Pointer
	if hr := comCall(uia, slotElementFromHandle, hwnd, uintptr(unsafe.Pointer(&rootEl))); int32(hr) < 0 || rootEl == nil {
		return ""
	}
	defer comRelease(rootEl)

	// Условие ControlType == Edit. VARIANT > 8 байт => на x64 передаётся по указателю
	// (Windows ABI), поэтому и by-value VARIANT-аргумент передаём как &variant.
	ctVar := ole.NewVariant(ole.VT_I4, int64(uiaEditControlTypeID))
	var cond unsafe.Pointer
	if hr := comCall(uia, slotCreatePropertyCondition,
		uintptr(uiaControlTypePropertyID), uintptr(unsafe.Pointer(&ctVar)), uintptr(unsafe.Pointer(&cond))); int32(hr) < 0 || cond == nil {
		return ""
	}
	defer comRelease(cond)

	// FindFirst(Descendants, cond) → address bar (первый Edit).
	var editEl unsafe.Pointer
	if hr := comCall(rootEl, slotElementFindFirst,
		uintptr(treeScopeDescendants), uintptr(cond), uintptr(unsafe.Pointer(&editEl))); int32(hr) < 0 || editEl == nil {
		return ""
	}
	defer comRelease(editEl)

	// GetCurrentPropertyValue(ValueValue) → VARIANT со строкой URL.
	var out ole.VARIANT
	ole.VariantInit(&out)
	if hr := comCall(editEl, slotElementGetCurrentPropertyValue,
		uintptr(uiaValueValuePropertyID), uintptr(unsafe.Pointer(&out))); int32(hr) < 0 {
		return ""
	}
	defer out.Clear()
	if out.VT != ole.VT_BSTR {
		return ""
	}
	return out.ToString()
}
