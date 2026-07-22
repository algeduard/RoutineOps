//go:build windows

package remotedesktop

import (
	"fmt"
	"image"
	"unsafe"

	"github.com/lxn/win"
)

// winCapturer снимает весь виртуальный экран (все мониторы) через GDI. Использует
// DIB-секцию (CreateDIBSection) с прямым доступом к пиксельному буферу: BitBlt
// пишет прямо в него, а Capture лишь конвертирует BGRA→RGBA. DXGI Desktop
// Duplication (быстрее, с дельтами) — оптимизация следующего этапа.
type winCapturer struct {
	x, y, w, h int
	srcDC      win.HDC
	memDC      win.HDC
	bmp        win.HBITMAP
	oldObj     win.HGDIOBJ
	bits       unsafe.Pointer // пиксели DIB (BGRA, top-down)
}

func newCapturer() (capturer, error) {
	x := int(win.GetSystemMetrics(win.SM_XVIRTUALSCREEN))
	y := int(win.GetSystemMetrics(win.SM_YVIRTUALSCREEN))
	w := int(win.GetSystemMetrics(win.SM_CXVIRTUALSCREEN))
	h := int(win.GetSystemMetrics(win.SM_CYVIRTUALSCREEN))
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("не удалось определить размеры виртуального экрана")
	}
	srcDC := win.GetDC(0) // HWND(0) = весь экран
	if srcDC == 0 {
		return nil, fmt.Errorf("GetDC(0) вернул 0")
	}
	memDC := win.CreateCompatibleDC(srcDC)
	if memDC == 0 {
		win.ReleaseDC(0, srcDC)
		return nil, fmt.Errorf("CreateCompatibleDC вернул 0")
	}
	bi := win.BITMAPINFOHEADER{
		BiSize:        uint32(unsafe.Sizeof(win.BITMAPINFOHEADER{})),
		BiWidth:       int32(w),
		BiHeight:      -int32(h), // < 0 => top-down DIB (строки сверху вниз, как в image.RGBA)
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: win.BI_RGB,
	}
	var bits unsafe.Pointer
	bmp := win.CreateDIBSection(memDC, &bi, win.DIB_RGB_COLORS, &bits, 0, 0)
	if bmp == 0 || bits == nil {
		win.DeleteDC(memDC)
		win.ReleaseDC(0, srcDC)
		return nil, fmt.Errorf("CreateDIBSection не выделил секцию")
	}
	oldObj := win.SelectObject(memDC, win.HGDIOBJ(bmp))
	return &winCapturer{
		x: x, y: y, w: w, h: h,
		srcDC: srcDC, memDC: memDC, bmp: bmp, oldObj: oldObj, bits: bits,
	}, nil
}

func (c *winCapturer) Bounds() (int, int) { return c.w, c.h }

func (c *winCapturer) Capture() (*image.RGBA, error) {
	if !win.BitBlt(c.memDC, 0, 0, int32(c.w), int32(c.h), c.srcDC, int32(c.x), int32(c.y), win.SRCCOPY) {
		return nil, fmt.Errorf("BitBlt не выполнен")
	}
	img := image.NewRGBA(image.Rect(0, 0, c.w, c.h))
	n := c.w * c.h * 4
	src := unsafe.Slice((*byte)(c.bits), n) // BGRA
	dst := img.Pix                          // RGBA
	for i := 0; i < n; i += 4 {
		dst[i+0] = src[i+2] // R <- B
		dst[i+1] = src[i+1] // G
		dst[i+2] = src[i+0] // B <- R
		dst[i+3] = 0xff     // A (в DIB не определён — делаем непрозрачным)
	}
	return img, nil
}

func (c *winCapturer) Close() {
	if c.memDC != 0 {
		if c.oldObj != 0 {
			win.SelectObject(c.memDC, c.oldObj)
		}
		win.DeleteDC(c.memDC)
	}
	if c.bmp != 0 {
		win.DeleteObject(win.HGDIOBJ(c.bmp))
	}
	if c.srcDC != 0 {
		win.ReleaseDC(0, c.srcDC)
	}
}
