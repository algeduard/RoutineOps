//go:build windows

package helpui

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"unsafe"

	"github.com/lxn/win"
)

// captureScreen снимает весь виртуальный экран (все мониторы) через GDI,
// уменьшает до maxDim по длинной стороне и кодирует в JPEG заданного качества.
// Возвращает и картинку (для превью в окне), и готовые байты (для заявки).
// Реализовано на lxn/win (уже в зависимостях lockui) — без новых библиотек.
func captureScreen(maxDim, quality int) (image.Image, []byte, error) {
	vx := win.GetSystemMetrics(win.SM_XVIRTUALSCREEN)
	vy := win.GetSystemMetrics(win.SM_YVIRTUALSCREEN)
	vw := win.GetSystemMetrics(win.SM_CXVIRTUALSCREEN)
	vh := win.GetSystemMetrics(win.SM_CYVIRTUALSCREEN)
	if vw <= 0 || vh <= 0 {
		return nil, nil, fmt.Errorf("виртуальный экран %dx%d", vw, vh)
	}

	hdcScreen := win.GetDC(0)
	if hdcScreen == 0 {
		return nil, nil, fmt.Errorf("GetDC(0) вернул 0")
	}
	defer win.ReleaseDC(0, hdcScreen)

	hdcMem := win.CreateCompatibleDC(hdcScreen)
	if hdcMem == 0 {
		return nil, nil, fmt.Errorf("CreateCompatibleDC вернул 0")
	}
	defer win.DeleteDC(hdcMem)

	hbmp := win.CreateCompatibleBitmap(hdcScreen, vw, vh)
	if hbmp == 0 {
		return nil, nil, fmt.Errorf("CreateCompatibleBitmap %dx%d вернул 0", vw, vh)
	}
	defer win.DeleteObject(win.HGDIOBJ(hbmp))

	old := win.SelectObject(hdcMem, win.HGDIOBJ(hbmp))
	// CAPTUREBLT — включить в кадр layered-окна (тултипы, оверлеи), без него их
	// на скриншоте нет, а сотрудник обычно показывает именно всплывшую ошибку.
	ok := win.BitBlt(hdcMem, 0, 0, vw, vh, hdcScreen, vx, vy, win.SRCCOPY|win.CAPTUREBLT)
	// Битмап обязан быть НЕ выбранным в DC на момент GetDIBits (требование WinAPI).
	win.SelectObject(hdcMem, old)
	if !ok {
		return nil, nil, fmt.Errorf("BitBlt не удался")
	}

	var bi win.BITMAPINFO
	bi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bi.BmiHeader))
	bi.BmiHeader.BiWidth = vw
	bi.BmiHeader.BiHeight = -vh // отрицательная высота = top-down, строки сверху вниз
	bi.BmiHeader.BiPlanes = 1
	bi.BmiHeader.BiBitCount = 32
	bi.BmiHeader.BiCompression = win.BI_RGB

	buf := make([]byte, int(vw)*int(vh)*4)
	if win.GetDIBits(hdcMem, hbmp, 0, uint32(vh), &buf[0], &bi, win.DIB_RGB_COLORS) == 0 {
		return nil, nil, fmt.Errorf("GetDIBits не удался")
	}

	// GDI отдаёт BGRA; альфа-канал у скриншота мусорный — заполняем непрозрачным.
	img := image.NewRGBA(image.Rect(0, 0, int(vw), int(vh)))
	for i := 0; i+3 < len(buf); i += 4 {
		img.Pix[i] = buf[i+2]
		img.Pix[i+1] = buf[i+1]
		img.Pix[i+2] = buf[i]
		img.Pix[i+3] = 0xFF
	}

	small := downscale(img, maxDim)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, small, &jpeg.Options{Quality: quality}); err != nil {
		return nil, nil, fmt.Errorf("jpeg: %w", err)
	}
	return small, out.Bytes(), nil
}

// downscale уменьшает картинку box-фильтром (среднее по блоку) до maxDim по
// длинной стороне. Ручная реализация ~30 строк, чтобы не тянуть x/image ради
// одного ресайза; для скриншота с текстом box-среднее даёт достаточное качество.
func downscale(src *image.RGBA, maxDim int) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	long := max(w, h)
	if long <= maxDim {
		return src
	}
	scale := float64(maxDim) / float64(long)
	nw := max(1, int(float64(w)*scale+0.5))
	nh := max(1, int(float64(h)*scale+0.5))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy0, sy1 := y*h/nh, (y+1)*h/nh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < nw; x++ {
			sx0, sx1 := x*w/nw, (x+1)*w/nw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, b, n uint32
			for sy := sy0; sy < sy1; sy++ {
				o := sy*src.Stride + sx0*4
				for sx := sx0; sx < sx1; sx++ {
					r += uint32(src.Pix[o])
					g += uint32(src.Pix[o+1])
					b += uint32(src.Pix[o+2])
					o += 4
					n++
				}
			}
			o := y*dst.Stride + x*4
			dst.Pix[o] = uint8(r / n)
			dst.Pix[o+1] = uint8(g / n)
			dst.Pix[o+2] = uint8(b / n)
			dst.Pix[o+3] = 0xFF
		}
	}
	return dst
}
