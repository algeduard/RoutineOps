import axios from "axios"
import { clearMeCache } from "@/lib/useMe"

export async function login(email: string, password: string): Promise<void> {
  await axios.post("/api/v1/auth/login", { email, password })
  sessionStorage.setItem("session", "1")
  clearMeCache() // на случай смены пользователя без перезагрузки страницы
}

export async function logout() {
  await axios.post("/api/v1/auth/logout").catch(() => {})
  sessionStorage.removeItem("session")
  clearMeCache()
}

export function isAuthenticated(): boolean {
  return sessionStorage.getItem("session") === "1"
}
