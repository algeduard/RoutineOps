import { useEffect, useState } from "react"
import api, { Capabilities } from "@/lib/api"

// Enterprise-возможности активной лицензии (GET /capabilities). В open-core роута нет
// (404) → трактуем все фичи как выключенные. Модульный кэш на сессию: набор меняется
// только при смене лицензии (тогда достаточно перезагрузить страницу).
let cached: Capabilities | null = null

const EMPTY: Capabilities = { software_removal: false, siem_export: false, audit_integrity: false, sso: false, compliance: false, cve_scan: false }

export function useCapabilities() {
  const [caps, setCaps] = useState<Capabilities>(cached ?? EMPTY)
  const [loading, setLoading] = useState(!cached)

  useEffect(() => {
    if (cached) {
      setLoading(false)
      return
    }
    api
      .get<Capabilities>("/capabilities")
      .then((r) => {
        cached = { ...EMPTY, ...r.data }
        setCaps(cached)
      })
      .catch(() => {
        // 404 (open-core) или сбой запроса → enterprise-фичи считаем выключенными.
        cached = EMPTY
        setCaps(EMPTY)
      })
      .finally(() => setLoading(false))
  }, [])

  return { caps, loading }
}

export function clearCapabilitiesCache() {
  cached = null
}
