import { useCallback, useEffect, useRef, useState } from "react"
import { useParams, useNavigate } from "react-router-dom"
import { ChevronLeft, MousePointer2, Eye, Maximize2, Loader2 } from "lucide-react"
import { Button } from "@/components/ui/button"

// Страница удалённого рабочего стола. Открывает WebSocket на сервер, рендерит
// приходящие JPEG-кадры в <canvas> и (в режиме управления) шлёт события мыши/
// клавиатуры. Cookie `token` уезжает автоматически при same-origin wss://.
// Транспорт до устройства и согласие пользователя — на стороне сервера/агента,
// см. docs/remote-desktop-design.md.

type Phase = "connecting" | "waiting" | "active" | "ended" | "error" | "denied"

// Браузерный MouseEvent.button (0=left,1=middle,2=right) → протокол
// RDInputEvent.button (0=left,1=right,2=middle).
const buttonMap: Record<number, number> = { 0: 0, 1: 2, 2: 1 }

export default function RemoteDesktop() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const controlRef = useRef(false)

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
        let m: any
        try { m = JSON.parse(ev.data) } catch { return }
        if (m.type === "ready") {
          ready = true
          setSize({ w: m.w, h: m.h })
          const c = canvasRef.current
          if (c) { c.width = m.w; c.height = m.h }
          setPhase("active")
        } else if (m.type === "status") {
          if (m.code === 2) { setPhase("denied") } // RD_STATUS_CODE_USER_DENIED
        } else if (m.type === "error") {
          setErrMsg(m.message || "ошибка")
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
      if (!ready) setErrMsg("не удалось подключиться — устройство офлайн или недоступно")
    }
    ws.onerror = () => {
      if (!ready) setErrMsg("не удалось подключиться — устройство офлайн или недоступно")
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
  const onMouseMove = (e: React.MouseEvent) => { if (controlRef.current) send({ t: "mouse_move", ...norm(e) }) }
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
          <ChevronLeft className="mr-1 h-4 w-4" /> К устройству
        </Button>
        <h1 className="text-lg font-semibold">Удалённый рабочий стол</h1>
        <PhasePill phase={phase} />
        <div className="ml-auto flex items-center gap-2">
          <Button
            variant={control ? "default" : "outline"}
            size="sm"
            disabled={phase !== "active"}
            onClick={() => setControl((v) => !v)}
            title={control ? "Только просмотр" : "Взять управление вводом"}
          >
            {control ? <MousePointer2 className="mr-1 h-4 w-4" /> : <Eye className="mr-1 h-4 w-4" />}
            {control ? "Управление" : "Просмотр"}
          </Button>
          <Button variant="outline" size="sm" disabled={phase !== "active"} onClick={enterFullscreen}>
            <Maximize2 className="mr-1 h-4 w-4" /> Во весь экран
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
            <p className="text-sm opacity-90">{phaseText(phase, errMsg)}</p>
            {(phase === "ended" || phase === "error" || phase === "denied") && (
              <Button variant="outline" size="sm" onClick={() => navigate(`/devices/${id}`)}>
                Вернуться к устройству
              </Button>
            )}
          </div>
        )}
      </div>

      {control && phase === "active" && (
        <p className="text-xs text-muted-foreground">
          Управление включено: движения мыши и нажатия клавиш передаются на устройство. Правый клик и системные
          сочетания перехватываются. Ctrl+Alt+Del перехватить из браузера нельзя (ограничение ОС).
        </p>
      )}
    </div>
  )
}

function PhasePill({ phase }: { phase: Phase }) {
  const map: Record<Phase, { label: string; cls: string }> = {
    connecting: { label: "Подключение", cls: "bg-yellow-500/15 text-yellow-600" },
    waiting: { label: "Ожидание устройства", cls: "bg-yellow-500/15 text-yellow-600" },
    active: { label: "В эфире", cls: "bg-green-500/15 text-green-600" },
    ended: { label: "Сеанс завершён", cls: "bg-muted text-muted-foreground" },
    error: { label: "Ошибка", cls: "bg-red-500/15 text-red-600" },
    denied: { label: "Отклонено пользователем", cls: "bg-red-500/15 text-red-600" },
  }
  const { label, cls } = map[phase]
  return <span className={`rounded px-2 py-0.5 text-xs font-medium ${cls}`}>{label}</span>
}

function phaseText(phase: Phase, errMsg: string): string {
  switch (phase) {
    case "connecting": return "Подключение…"
    case "waiting": return "Ожидаем, пока устройство поднимет сеанс…"
    case "ended": return "Сеанс завершён."
    case "denied": return "Пользователь на устройстве отклонил удалённый доступ."
    case "error": return errMsg || "Не удалось установить сеанс."
    default: return ""
  }
}
