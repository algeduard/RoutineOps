import { useSyncExternalStore } from "react"

// Глобальный флаг «текущему юзеру нужно включить MFA» (фича MFA enforce-by-policy, миграция
// 054). Источники правды — сервер: (1) /me отдаёт mfa_required, когда орг-политика требует MFA
// для роли юзера, а он её не включил; (2) любой заблокированный гейтом запрос отвечает 403 с
// заголовком X-MFA-Required / телом {"error":"mfa_required"}. Оба источника выставляют этот флаг,
// а баннер (MfaRequiredBanner) на него подписан. Сбрасывается после успешного включения MFA.
//
// Отдельный store (а не только кэш useMe) нужен, потому что политику могут включить ПОСЛЕ логина:
// тогда закэшированный /me ещё «чист», и единственный честный сигнал — 403 на реальном запросе.

let required = false
const listeners = new Set<() => void>()

export function setMfaRequired(v: boolean) {
  if (v === required) return
  required = v
  listeners.forEach((f) => f())
}

export function getMfaRequired(): boolean {
  return required
}

function subscribe(cb: () => void) {
  listeners.add(cb)
  return () => {
    listeners.delete(cb)
  }
}

// useMfaRequired подписывает компонент на изменения флага.
export function useMfaRequired(): boolean {
  return useSyncExternalStore(subscribe, () => required, () => required)
}

// isMfaRequiredResponse — распознаёт машиночитаемый сигнал mfa_required в ответе сервера
// (заголовок или тело), чтобы отличить наш гейт от прочих 403 (напр. requireRole).
export function isMfaRequiredResponse(resp: { headers?: Record<string, unknown>; data?: unknown } | undefined): boolean {
  if (!resp) return false
  const hdr = resp.headers?.["x-mfa-required"]
  if (hdr === "1" || hdr === 1) return true
  const data = resp.data as { error?: string } | string | undefined
  if (data && typeof data === "object" && data.error === "mfa_required") return true
  return false
}
