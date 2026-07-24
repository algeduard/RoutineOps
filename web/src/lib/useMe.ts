import { useEffect, useState } from "react"
import api from "@/lib/api"
import { setMfaRequired } from "@/lib/mfaGate"

export interface Me {
  id: string
  email: string
  name: string
  role: string
  // mfa_required: сервер (миграция 054) шлёт "true", когда орг-политика требует MFA для роли
  // юзера, а он её не включил. Строка, т.к. /me — плоская map[string]string. Отсутствует = не нужно.
  mfa_required?: string
}

// Модульный кэш: /me тянется один раз на сессию (роль редко меняется в рамках сессии),
// повторные маунты берут из кэша. clearMeCache() зовётся на logout.
let cached: Me | null = null

export function useMe() {
  const [me, setMe] = useState<Me | null>(cached)
  const [loading, setLoading] = useState(!cached)

  useEffect(() => {
    if (cached) return
    api
      .get<Me>("/me")
      .then((r) => {
        cached = r.data
        setMe(r.data)
        // Синхронизируем глобальный MFA-флаг с истиной сервера на момент логина (баннер).
        // Смену политики в середине сессии добирает interceptor по 403 на реальном запросе.
        setMfaRequired(r.data.mfa_required === "true")
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  return { me, isAdmin: me?.role === "it_admin", loading }
}

export function clearMeCache() {
  cached = null
}
