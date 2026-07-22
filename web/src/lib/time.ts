import { getLang } from "@/lib/i18n"

export function formatDistanceToNow(iso: string): string {
  const en = getLang() === "en"
  const diff = Date.now() - new Date(iso).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return en ? "just now" : "только что"
  const m = Math.floor(s / 60)
  if (m < 60) return en ? `${m} min ago` : `${m} мин. назад`
  const h = Math.floor(m / 60)
  if (h < 24) return en ? `${h} h ago` : `${h} ч. назад`
  const d = Math.floor(h / 24)
  return en ? `${d} d ago` : `${d} д. назад`
}
