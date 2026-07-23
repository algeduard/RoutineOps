import axios from "axios"
import { clearMeCache } from "@/lib/useMe"

// Результат шага-1 логина: либо сессия уже выдана (MFA выключена), либо требуется второй
// фактор — тогда возвращаем одноразовый challenge-токен для шага-2 (в теле, не в куке).
export type LoginResult = { mfaRequired: false } | { mfaRequired: true; mfaToken: string }

export async function login(email: string, password: string): Promise<LoginResult> {
  const res = await axios.post("/api/v1/auth/login", { email, password })
  if (res.data?.status === "mfa_required") {
    return { mfaRequired: true, mfaToken: res.data.mfa_token as string }
  }
  sessionStorage.setItem("session", "1")
  clearMeCache() // на случай смены пользователя без перезагрузки страницы
  return { mfaRequired: false }
}

// Шаг-2: проверка TOTP/recovery против challenge. При успехе сервер ставит httpOnly-куку.
export async function loginMfa(mfaToken: string, code: string): Promise<void> {
  await axios.post("/api/v1/auth/login/mfa", { mfa_token: mfaToken, code })
  sessionStorage.setItem("session", "1")
  clearMeCache()
}

export async function logout() {
  await axios.post("/api/v1/auth/logout").catch(() => {})
  sessionStorage.removeItem("session")
  clearMeCache()
}

export function isAuthenticated(): boolean {
  return sessionStorage.getItem("session") === "1"
}
