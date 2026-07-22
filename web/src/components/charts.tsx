import { useId } from "react"

// Лёгкие самодельные SVG-графики для телеметрии — без внешних библиотек (бандл уже
// >500КБ). preserveAspectRatio="none" растягивает под контейнер; штрихи держим
// 1px через vector-effect="non-scaling-stroke", иначе растяжение их искажает.

// MiniAreaChart — area/line по равномерно расположенным значениям (сервер отдаёт
// метрики уже корзинами, поэтому равномерный шаг корректен). max задаёт потолок оси
// Y (напр. 100 для процентов); по умолчанию — максимум ряда.
export function MiniAreaChart({ values, color, height = 44, max }: {
  values: number[]
  color: string
  height?: number
  max?: number
}) {
  const gid = useId()
  if (values.length === 0) {
    return (
      <div className="flex items-center justify-center text-xs text-muted-foreground" style={{ height }}>
        нет данных
      </div>
    )
  }
  const top = Math.max(1e-9, max ?? Math.max(...values))
  const n = values.length
  const coords = values.map((v, i) => {
    const x = n === 1 ? 0 : (i / (n - 1)) * 100
    const y = height - Math.min(1, Math.max(0, v / top)) * height
    return [x, y] as const
  })
  const line = coords.map(([x, y], i) => `${i === 0 ? "M" : "L"}${x.toFixed(2)},${y.toFixed(2)}`).join(" ")
  const area = `${line} L100,${height} L0,${height} Z`
  return (
    <svg viewBox={`0 0 100 ${height}`} preserveAspectRatio="none" width="100%" height={height} className="block">
      <defs>
        <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.28" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${gid})`} />
      <path
        d={line}
        fill="none"
        stroke={color}
        strokeWidth="1.5"
        vectorEffect="non-scaling-stroke"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  )
}

// HBar — одна горизонтальная полоса (топ приложений / активное время по дням).
export function HBar({ label, value, max, valueLabel, color }: {
  label: string
  value: number
  max: number
  valueLabel: string
  color: string
}) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0
  return (
    <div className="flex items-center gap-3">
      <div className="w-28 shrink-0 truncate text-sm text-foreground sm:w-40" title={label}>{label}</div>
      <div className="relative h-5 flex-1 overflow-hidden rounded bg-muted">
        <div className="absolute inset-y-0 left-0 rounded" style={{ width: `${pct}%`, backgroundColor: color }} />
      </div>
      <div className="w-16 shrink-0 text-right text-xs text-muted-foreground tabular-nums">{valueLabel}</div>
    </div>
  )
}

// RangeToggle — компактный переключатель диапазона (1ч/24ч, 7д/30д).
export function RangeToggle<T extends string>({ value, onChange, options }: {
  value: T
  onChange: (v: T) => void
  options: [T, string][]
}) {
  return (
    <div className="flex gap-0.5 rounded-md border border-border p-0.5">
      {options.map(([v, label]) => (
        <button
          key={v}
          type="button"
          onClick={() => onChange(v)}
          className={[
            "rounded px-2.5 py-1 text-xs font-medium transition-colors",
            value === v ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground",
          ].join(" ")}
        >
          {label}
        </button>
      ))}
    </div>
  )
}
