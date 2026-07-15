import { useEffect, useState } from "react"
import api from "@/lib/api"

export interface Me {
  id: string
  email: string
  name: string
  role: string
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
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  return { me, isAdmin: me?.role === "it_admin", loading }
}

export function clearMeCache() {
  cached = null
}
