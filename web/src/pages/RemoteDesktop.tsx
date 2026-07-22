import { useCallback, useEffect, useRef, useState } from "react"
import { useParams, useNavigate } from "react-router-dom"
import { ChevronLeft, MousePointer2, Eye, Maximize2, Loader2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { useT, type Msg } from "@/lib/i18n"

// Страница удалённого рабочего стола. Открывает WebSocket на сервер, рендерит
// приходящие JPEG-кадры в <canvas> и (в режиме управления) шлёт события мыши/
// клавиатуры. Cookie `token` уезжает автоматически при same-origin wss://.
// Транспорт до устройства и согласие пользователя — на стороне сервера/агента,
// см. docs/remote-desktop-design.md.

type Phase = "connecting" | "waiting" | "active" | "ended" | "error" | "denied"

// Браузерный MouseEvent.button (0=left,1=middle,2=right) → протокол
// RDInputEvent.button (0=left,1=right,2=middle).
const buttonMap: Record<number, number> = { 0: 0, 1: 2, 2: 1 }

const M = {
  errorFallback: { ru: "ошибка", en: "error" },
  connectFailed: {
    ru: "не удалось подключиться — устройство офлайн или недоступно",
    en: "failed to connect — the device is offline or unavailable",
  },
  backToDevice: { ru: "К устройству", en: "Back to device" },
  title: { ru: "Удалённый рабочий стол", en: "Remote desktop" },
  viewOnly: { ru: "Только просмотр", en: "View only" },
  takeControl: { ru: "Взять управление вводом", en: "Take input control" },
  control: { ru: "Управление", en: "Control" },
  view: { ru: "Просмотр", en: "View" },
  fullscreen: { ru: "Во весь экран", en: "Fullscreen" },
  returnToDevice: { ru: "Вернуться к устройству", en: "Return to device" },
  controlHint: {
    ru: "Управление включено: движения мыши и нажатия клавиш передаются на устройство. Правый клик и системные сочетания перехватываются. Ctrl+Alt+Del перехватить из браузера нельзя (ограничение ОС).",
    en: "Control is on: mouse movements and key presses are sent to the device. Right-click and system shortcuts are intercepted. Ctrl+Alt+Del cannot be captured from the browser (an OS limitation).",
  },
  pillConnecting: { ru: "Подключение", en: "Connecting" },
  pillWaiting: { ru: "Ожидание устройства", en: "Waiting for device" },
  pillActive: { ru: "В эфире", en: "Live" },
  pillEnded: { ru: "Сеанс завершён", en: "Session ended" },
  pillError: { ru: "Ошибка", en: "Error" },
  pillDenied: { ru: "Отклонено пользователем", en: "Denied by user" },
  textConnecting: { ru: "Подключение…", en: "Connecting…" },
  textWaiting: { ru: "Ожидаем, пока устройство поднимет сеанс…", en: "Waiting for the device to start the session…" },
  textEnded: { ru: "Сеанс завершён.", en: "Session ended." },
  textDenied: { ru: "Пользователь на устройстве отклонил удалённый доступ.", en: "The user on the device denied remote access." },
  textError: { ru: "Не удалось установить сеанс.", en: "Failed to establish the session." },
}

export default function RemoteDesktop() {
  const t = useT()
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const controlRef = useRef(false)
  const lastMoveRef = useRef(0)

  const [phase, setPhase] = useState<Phase>("connecting")
  const [errMsg, setErrMsg] = useState("")
  const [control, setControl] = useState(false)
  const [size, setSize] = useState<{ w: number; h: number } | null>(null)

  useEffect(() => {
    controlRef.current = control
  }, [control])

  // Установка WebSocket-соединения.
  useEffect(() => {
    if (!id) return
    const proto = location.protocol === "https:" ? "wss:" : "ws:"
    const ws = new WebSocket(`${proto}//${location.host}/api/v1/devices/${id}/remote-desktop`)
    ws.binaryType = "arraybuffer"
    wsRef.current = ws
    let ready = false

    ws.onmessage = async (ev) => {
      if (typeof ev.data === "string") {
        let m: { type?: string; w?: number; h?: number; code?: number; message?: string }
        try { m = JSON.parse(ev.data) } catch { return }
        if (m.type === "ready") {
          ready = true
          const w = m.w ?? 0, h = m.h ?? 0
          setSize({ w, h })
          const c = canvasRef.current
          if (c) { c.width = w; c.height = h }
          setPhase("active")
        } else if (m.type === "status") {
          if (m.code === 2) { setPhase("denied") } // RD_STATUS_CODE_USER_DENIED
        } else if (m.type === "error") {
          setErrMsg(m.message || t(M.errorFallback))
          setPhase("error")
        }
        return
      }
      // Бинарный кадр — JPEG.
      try {
        const bmp = await createImageBitmap(new Blob([ev.data]))
        const c = canvasRef.current
        if (c) {
          const ctx = c.getContext("2d")
          ctx?.drawImage(bmp, 0, 0, c.width, c.height)
        }
        bmp.close?.()
      } catch {
        /* повреждённый кадр — пропускаем */
      }
    }

    ws.onclose = () => {
      setPhase((p) => (p === "denied" || p === "error" ? p : ready ? "ended" : "error"))
      if (!ready) setErrMsg(t(M.connectFailed))
    }
    ws.onerror = () => {
      if (!ready) setErrMsg(t(M.connectFailed))
    }
    setPhase((p) => (p === "connecting" ? "waiting" : p))

    return () => {
      ws.onclose = null
      ws.close()
      wsRef.current = null
    }
  }, [id])

  const send = useCallback((obj: unknown) => {
    const ws = wsRef.current
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(obj))
  }, [])

  const norm = useCallback((e: React.MouseEvent) => {
    const c = canvasRef.current
    if (!c) return { x: 0, y: 0 }
    const r = c.getBoundingClientRect()
    return {
      x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)),
      y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)),
    }
  }, [])

  // Обработчики ввода активны только в режиме управления.
  const onMouseMove = (e: React.MouseEvent) => {
    if (!controlRef.current) return
    // Троттлинг ~30 Гц: браузер шлёт mousemove на каждый пиксель, без ограничения
    // это забивает серверный буфер ввода и вытесняет важные события (клавиши).
    const now = performance.now()
    if (now - lastMoveRef.current < 33) return
    lastMoveRef.current = now
    send({ t: "mouse_move", ...norm(e) })
  }
  const onMouseDown = (e: React.MouseEvent) => { if (controlRef.current) { e.preventDefault(); send({ t: "mouse_down", ...norm(e), button: buttonMap[e.button] ?? 0 }) } }
  const onMouseUp = (e: React.MouseEvent) => { if (controlRef.current) { e.preventDefault(); send({ t: "mouse_up", ...norm(e), button: buttonMap[e.button] ?? 0 }) } }
  const onWheel = (e: React.WheelEvent) => { if (controlRef.current) send({ t: "wheel", ...normWheel(e), delta: Math.round(-e.deltaY) }) }
  const normWheel = (e: React.WheelEvent) => {
    const c = canvasRef.current
    if (!c) return { x: 0, y: 0 }
    const r = c.getBoundingClientRect()
    return { x: (e.clientX - r.left) / r.width, y: (e.clientY - r.top) / r.height }
  }

  // Клавиатура: слушаем на всём окне, пока включено управление. keyCode —
  // legacy, но близок к Windows virtual-key, чего достаточно для MVP.
  useEffect(() => {
    if (!control) return
    const down = (e: KeyboardEvent) => { e.preventDefault(); send({ t: "key", code: e.keyCode, down: true, ctrl: e.ctrlKey, alt: e.altKey, shift: e.shiftKey, meta: e.metaKey }) }
    const up = (e: KeyboardEvent) => { e.preventDefault(); send({ t: "key", code: e.keyCode, down: false, ctrl: e.ctrlKey, alt: e.altKey, shift: e.shiftKey, meta: e.metaKey }) }
    window.addEventListener("keydown", down)
    window.addEventListener("keyup", up)
    return () => { window.removeEventListener("keydown", down); window.removeEventListener("keyup", up) }
  }, [control, send])

  const enterFullscreen = () => { canvasRef.current?.parentElement?.requestFullscreen?.() }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="sm" onClick={() => navigate(`/devices/${id}`)}>
          <ChevronLeft className="mr-1 h-4 w-4" /> {t(M.backToDevice)}
        </Button>
        <h1 className="text-lg font-semibold">{t(M.title)}</h1>
        <PhasePill phase={phase} />
        <div className="ml-auto flex items-center gap-2">
          <Button
            variant={control ? "default" : "outline"}
            size="sm"
            disabled={phase !== "active"}
            onClick={() => setControl((v) => !v)}
            title={control ? t(M.viewOnly) : t(M.takeControl)}
          >
            {control ? <MousePointer2 className="mr-1 h-4 w-4" /> : <Eye className="mr-1 h-4 w-4" />}
            {control ? t(M.control) : t(M.view)}
          </Button>
          <Button variant="outline" size="sm" disabled={phase !== "active"} onClick={enterFullscreen}>
            <Maximize2 className="mr-1 h-4 w-4" /> {t(M.fullscreen)}
          </Button>
        </div>
      </div>

      <div className="relative flex items-center justify-center rounded-lg border bg-black/90 overflow-hidden" style={{ minHeight: 360 }}>
        <canvas
          ref={canvasRef}
          className="max-h-[75vh] w-auto max-w-full"
          style={{ aspectRatio: size ? `${size.w} / ${size.h}` : undefined, cursor: control ? "none" : "default" }}
          onMouseMove={onMouseMove}
          onMouseDown={onMouseDown}
          onMouseUp={onMouseUp}
          onWheel={onWheel}
          onContextMenu={(e) => e.preventDefault()}
        />
        {phase !== "active" && (
          <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-black/70 text-white">
            {(phase === "connecting" || phase === "waiting") && <Loader2 className="h-8 w-8 animate-spin opacity-80" />}
            <p className="text-sm opacity-90">{phaseText(phase, errMsg, t)}</p>
            {(phase === "ended" || phase === "error" || phase === "denied") && (
              <Button variant="outline" size="sm" onClick={() => navigate(`/devices/${id}`)}>
                {t(M.returnToDevice)}
              </Button>
            )}
          </div>
        )}
      </div>

      {control && phase === "active" && (
        <p className="text-xs text-muted-foreground">
          {t(M.controlHint)}
        </p>
      )}
    </div>
  )
}

function PhasePill({ phase }: { phase: Phase }) {
  const t = useT()
  const map: Record<Phase, { label: Msg; cls: string }> = {
    connecting: { label: M.pillConnecting, cls: "bg-yellow-500/15 text-yellow-600" },
    waiting: { label: M.pillWaiting, cls: "bg-yellow-500/15 text-yellow-600" },
    active: { label: M.pillActive, cls: "bg-green-500/15 text-green-600" },
    ended: { label: M.pillEnded, cls: "bg-muted text-muted-foreground" },
    error: { label: M.pillError, cls: "bg-red-500/15 text-red-600" },
    denied: { label: M.pillDenied, cls: "bg-red-500/15 text-red-600" },
  }
  const { label, cls } = map[phase]
  return <span className={`rounded px-2 py-0.5 text-xs font-medium ${cls}`}>{t(label)}</span>
}

function phaseText(phase: Phase, errMsg: string, t: ReturnType<typeof useT>): string {
  switch (phase) {
    case "connecting": return t(M.textConnecting)
    case "waiting": return t(M.textWaiting)
    case "ended": return t(M.textEnded)
    case "denied": return t(M.textDenied)
    case "error": return errMsg || t(M.textError)
    default: return ""
  }
}
