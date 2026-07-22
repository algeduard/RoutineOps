import { useLang, setLang } from "@/lib/i18n"

// LangSwitcher — переключатель языка интерфейса RU⇄EN. Показывает язык, на который
// переключит (не текущий). Состояние глобальное, сохраняется в localStorage.
export function LangSwitcher({ className = "" }: { className?: string }) {
  const lang = useLang()
  const next = lang === "ru" ? "en" : "ru"
  return (
    <button
      type="button"
      onClick={() => setLang(next)}
      title={lang === "ru" ? "Switch to English" : "Переключить на русский"}
      aria-label="Language"
      className={
        "inline-flex items-center rounded-md border border-border px-2 py-1 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground " +
        className
      }
    >
      {next.toUpperCase()}
    </button>
  )
}
